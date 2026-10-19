package com.esp;

import org.springframework.batch.core.Job;
import org.springframework.batch.core.Step;
import org.springframework.batch.core.job.builder.JobBuilder;
import org.springframework.batch.core.step.builder.StepBuilder;
import org.springframework.batch.item.ItemReader;
import org.springframework.batch.item.ItemWriter;
import org.springframework.batch.item.database.JdbcCursorItemReader;
import org.springframework.batch.item.database.builder.JdbcCursorItemReaderBuilder;
import org.springframework.batch.core.configuration.annotation.StepScope;
import org.springframework.beans.factory.annotation.Value;
import org.springframework.context.annotation.Bean;
import org.springframework.context.annotation.Configuration;
import org.springframework.transaction.PlatformTransactionManager;
import org.springframework.batch.core.repository.JobRepository;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.jdbc.core.RowMapper;
import org.springframework.jdbc.core.namedparam.MapSqlParameterSource;
import org.springframework.jdbc.core.namedparam.NamedParameterJdbcTemplate;
import org.springframework.data.redis.core.StringRedisTemplate;

import javax.sql.DataSource;
import java.sql.PreparedStatement;
import java.sql.ResultSet;
import java.sql.SQLException;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

// Phase 26/58: Commercial-Grade Campaign Processing using Spring Batch
//
// Phase 58 fixes, found by reading this against the rest of the codebase:
//
// 1. The reader's SQL was "SELECT id, email, first_name, last_name FROM
//    contacts" -- no WHERE clause at all. Every campaign, for any org,
//    emailed EVERY contact in the entire contacts table, across every
//    org and every mailing list -- a real cross-tenant data leak, not a
//    narrow bug. Now a @StepScope bean bound to this job's actual
//    listId, and excludes contacts already recorded in
//    campaign_contact_sends for this campaign (the per-contact dispatch
//    ledger -- see schema.sql) and anyone on this org's suppression_list.
// 2. The writer pushed `{"To": "...", "Subject": "Commercial Campaign"}`
//    -- a hardcoded subject, no body, no org_id, no message_id, no from
//    address. nmail's Go outbound worker (startOutboundMTAWorker,
//    outbound_pool.go) requires org_id for its suppression/billing
//    checks and treats a missing body as an empty email, always. Now
//    builds the real payload shape outbound_pool.go actually reads, with
//    the campaign's real subject/body and a from address resolved from
//    the org's verified domain.
// 3. Nothing recorded that a contact had been sent to -- re-running (or
//    a retry after a partial failure) would have resent to everyone.
//    Writer now inserts into campaign_contact_sends per contact,
//    ON CONFLICT DO NOTHING, which is also what the reader's NOT EXISTS
//    check relies on.
@Configuration
public class BatchConfiguration {

    private static final Logger logger = LoggerFactory.getLogger(BatchConfiguration.class);

    public record Contact(String id, String email, String name) {}

    @Bean
    @StepScope
    public JdbcCursorItemReader<Contact> reader(
            DataSource dataSource,
            @Value("#{jobParameters['campaignId']}") String campaignId,
            @Value("#{jobParameters['listId']}") String listId,
            @Value("#{jobParameters['orgId']}") String orgId
    ) {
        return new JdbcCursorItemReaderBuilder<Contact>()
                .name("contactItemReader")
                .dataSource(dataSource)
                .sql(
                    "SELECT c.id, c.email, c.first_name FROM contacts c " +
                    "WHERE c.list_id = ?::uuid " +
                    "AND NOT EXISTS (SELECT 1 FROM campaign_contact_sends ccs WHERE ccs.campaign_id = ?::uuid AND ccs.contact_id = c.id) " +
                    "AND NOT EXISTS (SELECT 1 FROM suppression_list sl WHERE sl.org_id = ?::uuid AND sl.email = c.email)"
                )
                .preparedStatementSetter(ps -> {
                    ps.setString(1, listId);
                    ps.setString(2, campaignId);
                    ps.setString(3, orgId);
                })
                .rowMapper(new RowMapper<Contact>() {
                    @Override
                    public Contact mapRow(ResultSet rs, int rowNum) throws SQLException {
                        String name = rs.getString("first_name");
                        if (name == null) name = "";
                        return new Contact(rs.getString("id"), rs.getString("email"), name);
                    }
                })
                .build();
    }

    @Bean
    @StepScope
    public ItemWriter<Contact> writer(
            StringRedisTemplate redisTemplate,
            DataSource dataSource,
            @Value("#{jobParameters['campaignId']}") String campaignId,
            @Value("#{jobParameters['orgId']}") String orgId,
            @Value("#{jobParameters['subject']}") String subject,
            @Value("#{jobParameters['body']}") String body,
            @Value("#{jobParameters['fromAddress']}") String fromAddress
    ) {
        NamedParameterJdbcTemplate namedJdbc = new NamedParameterJdbcTemplate(dataSource);

        return chunk -> {
            for (Contact contact : chunk) {
                String messageId = "campaign_" + campaignId + "_" + contact.id();

                // Matches esp/main.go's SendEmailRequest JSON shape exactly --
                // outbound_pool.go reads message_id/org_id/to/from/subject/body
                // off this queue, not a Java-specific format.
                String payload = String.format(
                    "{\"message_id\":\"%s\",\"org_id\":\"%s\",\"to\":\"%s\",\"from\":\"%s\",\"subject\":%s,\"body\":%s}",
                    jsonEscape(messageId),
                    jsonEscape(orgId),
                    jsonEscape(contact.email()),
                    jsonEscape(fromAddress),
                    jsonString(subject),
                    jsonString(body)
                );
                redisTemplate.opsForList().leftPush("email_outbound_queue", payload);

                MapSqlParameterSource params = new MapSqlParameterSource()
                        .addValue("campaignId", campaignId)
                        .addValue("contactId", contact.id());
                namedJdbc.update(
                    "INSERT INTO campaign_contact_sends (campaign_id, contact_id) VALUES (:campaignId::uuid, :contactId::uuid) ON CONFLICT DO NOTHING",
                    params
                );
            }
            logger.info("Spring Batch: Campaign {}: dispatched chunk of {} contacts.", campaignId, chunk.size());
        };
    }

    private static String jsonString(String value) {
        return value == null ? "null" : "\"" + jsonEscape(value) + "\"";
    }

    private static String jsonEscape(String value) {
        if (value == null) return "";
        return value.replace("\\", "\\\\").replace("\"", "\\\"").replace("\n", "\\n").replace("\r", "");
    }

    @Bean
    public Step campaignStep(JobRepository jobRepository, PlatformTransactionManager transactionManager,
                             ItemReader<Contact> reader, ItemWriter<Contact> writer) {
        return new StepBuilder("campaignStep", jobRepository)
                .<Contact, Contact>chunk(1000, transactionManager)
                .reader(reader)
                .writer(writer)
                .build();
    }

    @Bean
    public Job campaignJob(JobRepository jobRepository, Step campaignStep) {
        return new JobBuilder("campaignJob", jobRepository)
                .start(campaignStep)
                .build();
    }
}
