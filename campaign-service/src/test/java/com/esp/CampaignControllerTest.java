package com.esp;

import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.springframework.data.redis.core.StringRedisTemplate;
import org.springframework.http.HttpStatus;
import org.springframework.http.ResponseEntity;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.test.util.ReflectionTestUtils;

import java.util.Map;

import static org.junit.jupiter.api.Assertions.assertEquals;
import java.util.concurrent.TimeUnit;

import static org.mockito.ArgumentMatchers.anyLong;
import static org.mockito.ArgumentMatchers.anyString;
import static org.mockito.ArgumentMatchers.eq;
import static org.mockito.Mockito.*;

// Phase 60: real unit tests for CampaignController -- no auth check and no
// UUID-vs-String handling existed here before Phase 58 at all, and both
// were real bugs confirmed only by actually running the service (see
// Phase 58's commit). These lock the fixed behavior in place.
class CampaignControllerTest {

    private CampaignController controller;
    private JdbcTemplate jdbcTemplate;
    private StringRedisTemplate redisTemplate;

    @BeforeEach
    void setUp() {
        controller = new CampaignController();
        jdbcTemplate = mock(JdbcTemplate.class);
        redisTemplate = mock(StringRedisTemplate.class);
        ReflectionTestUtils.setField(controller, "jdbcTemplate", jdbcTemplate);
        ReflectionTestUtils.setField(controller, "redisTemplate", redisTemplate);
        ReflectionTestUtils.setField(controller, "mailHostname", "mail.nmail.local");
        // jobLauncher/campaignJob are only touched inside the fire-and-forget
        // Thread processCampaign() starts; leaving them null is fine for the
        // synchronous parts these tests actually assert on.
    }

    @Test
    void sendCampaign_missingApiKey_returns401WithoutTouchingTheDatabase() {
        ResponseEntity<String> response = controller.sendCampaign("campaign-1", null, null);

        assertEquals(HttpStatus.UNAUTHORIZED, response.getStatusCode());
        verifyNoInteractions(jdbcTemplate);
    }

    @Test
    void sendCampaign_blankApiKey_returns401() {
        ResponseEntity<String> response = controller.sendCampaign("campaign-1", "   ", null);

        assertEquals(HttpStatus.UNAUTHORIZED, response.getStatusCode());
        verifyNoInteractions(jdbcTemplate);
    }

    @Test
    void sendCampaign_apiKeyNotFoundInDatabase_returns401() {
        when(jdbcTemplate.queryForObject(anyString(), eq(String.class), anyString()))
                .thenThrow(new RuntimeException("no rows"));

        ResponseEntity<String> response = controller.sendCampaign("campaign-1", "bogus-key", null);

        assertEquals(HttpStatus.UNAUTHORIZED, response.getStatusCode());
        assertEquals("{\"message\": \"Invalid API key\"}", response.getBody());
    }

    @Test
    void sendCampaign_neverTrustsAnOrgIdFromTheCaller() {
        // Phase 58's actual fix: org_id used to come straight from a
        // ?org_id= query parameter the caller supplied -- the signature no
        // longer accepts one at all. This is a compile-time guarantee this
        // test exercises directly: there is no parameter to pass a forged
        // org_id through, only the API key, which resolveFromAddress/the
        // campaign lookup below both key off of.
        when(jdbcTemplate.queryForObject(eq("SELECT s.org_id FROM api_keys a JOIN sub_accounts s ON a.sub_account_id = s.id WHERE a.key_hash = ?"),
                eq(String.class), eq("real-key")))
                .thenReturn("org-999");
        when(jdbcTemplate.queryForMap(anyString(), eq("campaign-1"), eq("org-999")))
                .thenThrow(new RuntimeException("not found for this org")); // simulates campaign belonging to a different org

        ResponseEntity<String> response = controller.sendCampaign("campaign-1", "real-key", null);

        // The campaign lookup is scoped to the API key's own resolved org,
        // not any value the caller could have supplied -- a campaign that
        // doesn't belong to that org is a 500/not-found, never a launch.
        assertEquals(HttpStatus.INTERNAL_SERVER_ERROR, response.getStatusCode());
    }

    @Test
    void sendCampaign_idempotencyKeyRejectsADuplicateRequest() {
        when(jdbcTemplate.queryForObject(anyString(), eq(String.class), anyString())).thenReturn("org-1");
        org.springframework.data.redis.core.ValueOperations<String, String> valueOps = mock(org.springframework.data.redis.core.ValueOperations.class);
        when(redisTemplate.opsForValue()).thenReturn(valueOps);
        when(valueOps.setIfAbsent(eq("idempotency:dup-key"), eq("executing"), anyLong(), eq(TimeUnit.HOURS))).thenReturn(false);

        ResponseEntity<String> response = controller.sendCampaign("campaign-1", "real-key", "dup-key");

        assertEquals(HttpStatus.CONFLICT, response.getStatusCode());
    }

    @Test
    void sendCampaign_campaignNotInDraftStatus_rejectsWithoutLaunching() {
        when(jdbcTemplate.queryForObject(anyString(), eq(String.class), anyString())).thenReturn("org-1");
        Map<String, Object> campaignRow = Map.of("status", "sending", "list_id", "list-1");
        when(jdbcTemplate.queryForMap(anyString(), eq("campaign-1"), eq("org-1"))).thenReturn(campaignRow);

        ResponseEntity<String> response = controller.sendCampaign("campaign-1", "real-key", null);

        assertEquals(HttpStatus.BAD_REQUEST, response.getStatusCode());
        // Must never flip the campaign to 'sending' when it already isn't 'draft'.
        verify(jdbcTemplate, never()).update(anyString(), eq("campaign-1"));
    }
}
