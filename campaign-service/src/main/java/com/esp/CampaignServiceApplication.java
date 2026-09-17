package com.esp;

import org.springframework.boot.SpringApplication;
import org.springframework.boot.autoconfigure.SpringBootApplication;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.boot.context.event.ApplicationReadyEvent;
import org.springframework.context.event.EventListener;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

@SpringBootApplication
public class CampaignServiceApplication {
    
    private static final Logger logger = LoggerFactory.getLogger(CampaignServiceApplication.class);
    
    @Autowired
    private JdbcTemplate jdbcTemplate;

    public static void main(String[] args) {
        SpringApplication.run(CampaignServiceApplication.class, args);
    }

    // Phase 25: The Java Bootloader (Recover orphaned campaigns)
    @EventListener(ApplicationReadyEvent.class)
    public void runBootloader() {
        logger.info("Java Bootloader: Scanning for orphaned campaigns...");
        try {
            int count = jdbcTemplate.update("UPDATE campaigns SET status = 'draft' WHERE status = 'sending'");
            logger.info("Java Bootloader: Resumed " + count + " orphaned campaigns.");
        } catch (Exception e) {
            logger.error("Failed to run bootloader", e);
        }
    }
}
