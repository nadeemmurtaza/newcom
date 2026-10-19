package com.esp;

import org.springframework.web.bind.annotation.*;
import org.springframework.http.ResponseEntity;
import org.springframework.http.HttpStatus;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.beans.factory.annotation.Value;
import org.springframework.data.redis.core.StringRedisTemplate;
import org.springframework.jdbc.core.JdbcTemplate;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

import java.util.concurrent.TimeUnit;
import java.util.Map;

// Phase 25/58: Campaign Dispatch API
//
// Phase 58: this endpoint had no authentication at all -- anyone who
// could reach this service could POST /api/campaigns/{id}/send with any
// org_id they liked and launch that org's campaign. Added the same
// api_keys-based auth nmail's Go authMiddleware uses (same table, same
// literal key_hash comparison) rather than inventing a second scheme; the
// org_id is now resolved from the API key, never trusted from the
// caller's own query parameter.
@RestController
@RequestMapping("/api/campaigns")
public class CampaignController {

    private static final Logger logger = LoggerFactory.getLogger(CampaignController.class);

    @Autowired
    private StringRedisTemplate redisTemplate;

    @Autowired
    private JdbcTemplate jdbcTemplate;

    @Autowired
    private org.springframework.batch.core.launch.JobLauncher jobLauncher;

    @Autowired
    private org.springframework.batch.core.Job campaignJob;

    @Value("${MAIL_HOSTNAME:mail.nmail.local}")
    private String mailHostname;

    @PostMapping("/{id}/send")
    public ResponseEntity<String> sendCampaign(@PathVariable("id") String campaignId,
                                             @RequestHeader(value = "Authorization", required = false) String apiKey,
                                             @RequestHeader(value = "Idempotency-Key", required = false) String idempotencyKey) {

        if (apiKey == null || apiKey.isBlank()) {
            return ResponseEntity.status(HttpStatus.UNAUTHORIZED).body("{\"message\": \"Missing API key\"}");
        }

        String orgId;
        try {
            orgId = jdbcTemplate.queryForObject(
                "SELECT s.org_id FROM api_keys a JOIN sub_accounts s ON a.sub_account_id = s.id WHERE a.key_hash = ?",
                String.class, apiKey
            );
        } catch (Exception e) {
            return ResponseEntity.status(HttpStatus.UNAUTHORIZED).body("{\"message\": \"Invalid API key\"}");
        }

        // Phase 25: API Idempotency via Redis
        if (idempotencyKey != null && !idempotencyKey.isEmpty()) {
            Boolean success = redisTemplate.opsForValue().setIfAbsent("idempotency:" + idempotencyKey, "executing", 24, TimeUnit.HOURS);
            if (Boolean.FALSE.equals(success)) {
                logger.warn("Idempotent request rejected: {}", idempotencyKey);
                return ResponseEntity.status(HttpStatus.CONFLICT).body("{\"message\": \"Request already processed\"}");
            }
        }

        try {
            Map<String, Object> campaign = jdbcTemplate.queryForMap(
                "SELECT status, list_id, subject, body_text, body_html FROM campaigns WHERE id = ?::uuid AND org_id = ?::uuid",
                campaignId, orgId);

            if (!"draft".equals(campaign.get("status"))) {
                return ResponseEntity.badRequest().body("{\"message\": \"Campaign is not in draft status\"}");
            }

            // Phase 58: the Postgres JDBC driver returns UUID columns as
            // java.util.UUID, not String -- a blind (String) cast throws
            // ClassCastException at runtime for every single call. Object
            // ::toString() works for both UUID and String alike.
            Object listIdValue = campaign.get("list_id");
            String listId = listIdValue == null ? null : listIdValue.toString();
            Object subjectValue = campaign.get("subject");
            String subject = subjectValue == null ? null : subjectValue.toString();
            Object bodyValue = campaign.get("body_text") != null ? campaign.get("body_text") : campaign.get("body_html");
            String body = bodyValue == null ? null : bodyValue.toString();
            if (listId == null) {
                return ResponseEntity.badRequest().body("{\"message\": \"Campaign has no mailing list\"}");
            }

            String fromAddress = resolveFromAddress(orgId);

            jdbcTemplate.update("UPDATE campaigns SET status = 'sending' WHERE id = ?::uuid", campaignId);

            new Thread(() -> processCampaign(campaignId, listId, orgId, subject, body, fromAddress)).start();

            return ResponseEntity.ok("{\"message\": \"Campaign queued for sending\"}");
        } catch (Exception e) {
            logger.error("Error launching campaign", e);
            return ResponseEntity.status(HttpStatus.INTERNAL_SERVER_ERROR).body("{\"message\": \"Database error\"}");
        }
    }

    /**
     * Resolves a real "from" address off the org's own verified domain,
     * the same way esp's Go side (journey_engine.go, ab_test_engine.go)
     * does -- never a hardcoded placeholder domain nobody owns.
     */
    private String resolveFromAddress(String orgId) {
        try {
            String domain = jdbcTemplate.queryForObject(
                "SELECT domain_name FROM domains WHERE org_id = ?::uuid AND is_verified = true LIMIT 1",
                String.class, orgId
            );
            if (domain != null && !domain.isBlank()) {
                return "campaigns@" + domain;
            }
        } catch (Exception ignored) {
            // No verified domain for this org yet -- fall back below.
        }
        return "campaigns@" + mailHostname;
    }

    private void processCampaign(String campaignId, String listId, String orgId, String subject, String body, String fromAddress) {
        logger.info("Java Worker: Campaign {}: Starting Spring Batch Processing...", campaignId);
        try {
            org.springframework.batch.core.JobParameters params = new org.springframework.batch.core.JobParametersBuilder()
                    .addString("campaignId", campaignId)
                    .addString("listId", listId)
                    .addString("orgId", orgId)
                    .addString("subject", subject == null ? "" : subject)
                    .addString("body", body == null ? "" : body)
                    .addString("fromAddress", fromAddress)
                    .addLong("time", System.currentTimeMillis())
                    .toJobParameters();

            jobLauncher.run(campaignJob, params);

            jdbcTemplate.update("UPDATE campaigns SET status = 'completed' WHERE id = ?::uuid", campaignId);
            logger.info("Java Worker: Campaign {}: Spring Batch Processing complete.", campaignId);
        } catch (Exception e) {
            logger.error("Spring Batch Job Failed for campaign {}", campaignId, e);
            jdbcTemplate.update("UPDATE campaigns SET status = 'draft' WHERE id = ?::uuid", campaignId);
        }
    }
}
