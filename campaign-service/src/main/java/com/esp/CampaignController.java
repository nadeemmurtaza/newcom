package com.esp;

import org.springframework.web.bind.annotation.*;
import org.springframework.http.ResponseEntity;
import org.springframework.http.HttpStatus;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.data.redis.core.StringRedisTemplate;
import org.springframework.jdbc.core.JdbcTemplate;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

import java.util.concurrent.TimeUnit;
import java.util.Map;
import java.util.UUID;

@RestController
@RequestMapping("/api/campaigns")
public class CampaignController {

    private static final Logger logger = LoggerFactory.getLogger(CampaignController.class);

    @Autowired
    private StringRedisTemplate redisTemplate;

    @Autowired
    private JdbcTemplate jdbcTemplate;

    @PostMapping("/{id}/send")
    public ResponseEntity<String> sendCampaign(@PathVariable("id") String campaignId,
                                             @RequestParam("org_id") String orgId,
                                             @RequestHeader(value = "Idempotency-Key", required = false) String idempotencyKey) {
        
        // Phase 25: API Idempotency via Redis
        if (idempotencyKey != null && !idempotencyKey.isEmpty()) {
            Boolean success = redisTemplate.opsForValue().setIfAbsent("idempotency:" + idempotencyKey, "executing", 24, TimeUnit.HOURS);
            if (Boolean.FALSE.equals(success)) {
                logger.warn("Idempotent request rejected: {}", idempotencyKey);
                return ResponseEntity.status(HttpStatus.CONFLICT).body("{\"message\": \"Request already processed\"}");
            }
        }

        try {
            // Verify Campaign
            Map<String, Object> campaign = jdbcTemplate.queryForMap(
                "SELECT status, list_id FROM campaigns WHERE id = ?::uuid AND org_id = ?::uuid", campaignId, orgId);
            
            if (!"draft".equals(campaign.get("status"))) {
                return ResponseEntity.badRequest().body("Campaign is not in draft status");
            }

            // Update to sending
            jdbcTemplate.update("UPDATE campaigns SET status = 'sending' WHERE id = ?::uuid", campaignId);

            // Spin off processing into an async thread (simulating processCampaign in Java)
            new Thread(() -> processCampaign(campaignId, (String) campaign.get("list_id"), orgId)).start();

            return ResponseEntity.ok("{\"message\": \"Campaign queued for sending\"}");
        } catch (Exception e) {
            logger.error("Error launching campaign", e);
            return ResponseEntity.status(HttpStatus.INTERNAL_SERVER_ERROR).body("Database error");
        }
    }

    @Autowired
    private org.springframework.batch.core.launch.JobLauncher jobLauncher;

    @Autowired
    private org.springframework.batch.core.Job campaignJob;

    private void processCampaign(String campaignId, String listId, String orgId) {
        logger.info("Java Worker: Campaign {}: Starting Commercial Spring Batch Processing...", campaignId);
        try {
            org.springframework.batch.core.JobParameters params = new org.springframework.batch.core.JobParametersBuilder()
                    .addString("campaignId", campaignId)
                    .addLong("time", System.currentTimeMillis())
                    .toJobParameters();
            
            jobLauncher.run(campaignJob, params);
            
            jdbcTemplate.update("UPDATE campaigns SET status = 'completed' WHERE id = ?::uuid", campaignId);
            logger.info("Java Worker: Campaign {}: Spring Batch Processing complete.", campaignId);
        } catch (Exception e) {
            logger.error("Spring Batch Job Failed", e);
        }
    }
}
