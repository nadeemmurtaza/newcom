package com.esp;

import org.springframework.batch.core.Job;
import org.springframework.batch.core.Step;
import org.springframework.batch.core.job.builder.JobBuilder;
import org.springframework.batch.core.step.builder.StepBuilder;
import org.springframework.batch.item.ItemReader;
import org.springframework.batch.item.ItemWriter;
import org.springframework.batch.item.database.JdbcCursorItemReader;
import org.springframework.batch.item.database.builder.JdbcCursorItemReaderBuilder;
import org.springframework.context.annotation.Bean;
import org.springframework.context.annotation.Configuration;
import org.springframework.transaction.PlatformTransactionManager;
import org.springframework.batch.core.repository.JobRepository;
import org.springframework.jdbc.core.RowMapper;
import org.springframework.data.redis.core.StringRedisTemplate;

import javax.sql.DataSource;
import java.sql.ResultSet;
import java.sql.SQLException;
import java.util.UUID;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

// Phase 26: Commercial-Grade Campaign Processing using Spring Batch
@Configuration
public class BatchConfiguration {

    private static final Logger logger = LoggerFactory.getLogger(BatchConfiguration.class);

    public record Contact(String id, String email, String name) {}

    @Bean
    public JdbcCursorItemReader<Contact> reader(DataSource dataSource) {
        return new JdbcCursorItemReaderBuilder<Contact>()
                .name("contactItemReader")
                .dataSource(dataSource)
                .sql("SELECT id, email, first_name, last_name FROM contacts")
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
    public ItemWriter<Contact> writer(StringRedisTemplate redisTemplate) {
        return chunk -> {
            for (Contact contact : chunk) {
                // Simulate constructing a SendEmailRequest payload
                String payload = String.format("{\"To\":\"%s\", \"Subject\":\"Commercial Campaign\"}", contact.email());
                // Push to Redis Outbound Queue
                redisTemplate.opsForList().leftPush("email_outbound_queue", payload);
            }
            logger.info("Spring Batch: Successfully wrote chunk of {} contacts to Redis Pub/Sub.", chunk.size());
        };
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
