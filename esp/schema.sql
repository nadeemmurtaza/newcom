-- Drop existing tables to recreate multi-tenant schema
DROP TABLE IF EXISTS events CASCADE;
DROP TABLE IF EXISTS suppression_list CASCADE;
DROP TABLE IF EXISTS api_keys CASCADE;
DROP TABLE IF EXISTS domains CASCADE;
DROP TABLE IF EXISTS users CASCADE;
DROP TABLE IF EXISTS organization_users CASCADE;
DROP TABLE IF EXISTS sub_accounts CASCADE;
DROP TABLE IF EXISTS organizations CASCADE;

CREATE TABLE organizations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(255) NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    billing_plan VARCHAR(50) DEFAULT 'free',
    
    -- Phase 36: Enterprise Multi-Tenancy
    tier VARCHAR(50) DEFAULT 'Free', -- Free, Pro, Enterprise
    monthly_email_quota INT DEFAULT 10000,
    data_region VARCHAR(50) DEFAULT 'US-EAST', -- US-EAST, EU-CENTRAL

    -- Phase 37: Enterprise Organization System
    parent_org_id UUID REFERENCES organizations(id), -- Hierarchical Reseller Model
    sso_enforced BOOLEAN DEFAULT FALSE,
    saml_metadata_url VARCHAR(500)
);

CREATE TABLE users (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email VARCHAR(255) UNIQUE NOT NULL,
    password_hash VARCHAR(255) NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE organization_users (
    org_id UUID REFERENCES organizations(id) ON DELETE CASCADE,
    user_id UUID REFERENCES users(id) ON DELETE CASCADE,
    PRIMARY KEY (org_id, user_id)
);

-- Permission-Based Access Control (PBAC)
CREATE TABLE organization_user_permissions (
    org_id UUID,
    user_id UUID,
    permission VARCHAR(100) NOT NULL, -- e.g., 'domains:read', 'domains:write', 'analytics:read', 'keys:write'
    PRIMARY KEY (org_id, user_id, permission),
    FOREIGN KEY (org_id, user_id) REFERENCES organization_users(org_id, user_id) ON DELETE CASCADE
);

CREATE TABLE domains (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID REFERENCES organizations(id) ON DELETE CASCADE,
    domain_name VARCHAR(255) UNIQUE NOT NULL,
    is_verified BOOLEAN DEFAULT FALSE,
    custom_tracking_url VARCHAR(255), -- e.g., 'track.customer.com'
    brand_logo_url VARCHAR(255),
    brand_primary_color VARCHAR(7),
    
    -- Phase 38: Enterprise Domain System (CNAME & Tracking)
    status VARCHAR(50) DEFAULT 'PENDING',
    cname_target VARCHAR(255),
    dkim_public_key TEXT,
    dkim_private_key TEXT,
    
    -- Phase 41: White-Label Portal Domain
    portal_domain VARCHAR(255) UNIQUE,
    
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE sub_accounts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID REFERENCES organizations(id) ON DELETE CASCADE,
    domain_id UUID REFERENCES domains(id) ON DELETE SET NULL,
    account_name VARCHAR(100) NOT NULL, -- e.g., 'Marketing', 'Transactional'
    status VARCHAR(50) DEFAULT 'active', -- Phase 30: active, suspended_for_security
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE api_keys (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    sub_account_id UUID REFERENCES sub_accounts(id) ON DELETE CASCADE,
    key_hash VARCHAR(255) UNIQUE NOT NULL,
    name VARCHAR(100),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    last_used_at TIMESTAMP WITH TIME ZONE
);

CREATE TABLE IF NOT EXISTS caa_policies (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID REFERENCES organizations(id),
    app_name VARCHAR(255) NOT NULL, 
    cel_expression TEXT NOT NULL, 
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- ==========================================
-- Phase 35: Enterprise Team Management
-- ==========================================

-- MFA Support
ALTER TABLE users ADD COLUMN IF NOT EXISTS mfa_enabled BOOLEAN DEFAULT FALSE;
ALTER TABLE users ADD COLUMN IF NOT EXISTS mfa_secret VARCHAR(255);

ALTER TABLE sub_accounts ADD COLUMN IF NOT EXISTS mfa_enabled BOOLEAN DEFAULT FALSE;
ALTER TABLE sub_accounts ADD COLUMN IF NOT EXISTS mfa_secret VARCHAR(255);

CREATE TABLE IF NOT EXISTS audit_logs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID REFERENCES organizations(id),
    actor_id UUID NOT NULL, -- user_id or sub_account_id
    action VARCHAR(255) NOT NULL, -- e.g. "DOMAIN_VERIFIED", "CAMPAIGN_QUEUED"
    ip_address VARCHAR(45),
    payload JSONB,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS approval_requests (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID REFERENCES organizations(id),
    requester_id UUID REFERENCES sub_accounts(id),
    status VARCHAR(50) DEFAULT 'PENDING_APPROVAL', -- PENDING_APPROVAL, APPROVED, REJECTED
    campaign_payload JSONB NOT NULL,
    approver_id UUID, -- user_id or sub_account_id
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    resolved_at TIMESTAMP
);

CREATE TABLE suppression_list (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID REFERENCES organizations(id) ON DELETE CASCADE,
    email VARCHAR(255) NOT NULL,
    reason TEXT,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (org_id, email) -- Suppression lists are isolated per organization
);

CREATE TABLE events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    sub_account_id UUID REFERENCES sub_accounts(id) ON DELETE CASCADE,
    message_id VARCHAR(255) NOT NULL,
    event_type VARCHAR(50) NOT NULL,
    metadata JSONB,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- Phase 6: Mailbox System
CREATE TABLE mailboxes (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID REFERENCES organizations(id) ON DELETE CASCADE,
    email_address VARCHAR(255) UNIQUE NOT NULL,
    
    -- Phase 39: Enterprise Mailboxes
    is_catch_all BOOLEAN DEFAULT FALSE,
    daily_send_limit INT DEFAULT 2000,
    
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE email_aliases (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    mailbox_id UUID REFERENCES mailboxes(id) ON DELETE CASCADE,
    alias_address VARCHAR(255) UNIQUE NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE app_passwords (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    mailbox_id UUID REFERENCES mailboxes(id) ON DELETE CASCADE,
    token_hash VARCHAR(255) UNIQUE NOT NULL,
    device_name VARCHAR(100),
    last_used_at TIMESTAMP,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE folders (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    mailbox_id UUID REFERENCES mailboxes(id) ON DELETE CASCADE,
    name VARCHAR(100) NOT NULL, -- 'INBOX', 'SENT', 'DRAFTS', 'OUTBOX', 'SPAM', 'TRASH', 'ARCHIVE'
    is_system BOOLEAN DEFAULT FALSE, -- Phase 42
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (mailbox_id, name)
);

CREATE TABLE messages (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID REFERENCES organizations(id), -- Added for RLS
    mailbox_id UUID REFERENCES mailboxes(id) ON DELETE CASCADE,
    folder_id UUID REFERENCES folders(id),
    sender VARCHAR(255) NOT NULL,
    recipients TEXT NOT NULL,
    subject VARCHAR(255),
    snippet VARCHAR(255),
    body_text TEXT, -- Phase 50: Inbound SMTP body storage (plain text)
    body_html TEXT, -- Phase 50: Inbound SMTP body storage (HTML)
    colossus_blob_id VARCHAR(255), -- Phase 27: Pointer to raw bytes in Colossus
    smart_replies JSONB, -- Phase 27: AI Quick Replies
    category VARCHAR(50) DEFAULT 'primary',
    thread_id VARCHAR(255),
    phishing_flag BOOLEAN DEFAULT FALSE,
    is_read BOOLEAN DEFAULT FALSE,
    is_starred BOOLEAN DEFAULT FALSE,
    search_vector tsvector, -- LSM Search Index simulation
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- Phase 36: Engine-Level Tenant Isolation (RLS)
ALTER TABLE messages ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation_policy ON messages
    USING (org_id = current_setting('app.current_org_id', true)::uuid);


CREATE INDEX messages_search ON messages USING GIN (search_vector);

CREATE TABLE attachments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    message_id UUID REFERENCES messages(id) ON DELETE CASCADE,
    filename VARCHAR(255) NOT NULL,
    content_type VARCHAR(100),
    size_bytes INT,
    storage_path VARCHAR(255) NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- Phase 7: Templates
CREATE TABLE templates (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID REFERENCES organizations(id) ON DELETE CASCADE,
    name VARCHAR(255) NOT NULL,
    subject_template TEXT,
    html_content TEXT,
    text_content TEXT,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- Phase 8 & Phase 49: Outbound Webhooks & Event Firehose
CREATE TABLE webhooks (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID REFERENCES organizations(id) ON DELETE CASCADE,
    endpoint_url VARCHAR(500) NOT NULL,
    secret_key VARCHAR(100) NOT NULL, -- Used to sign the webhook payload via HMAC
    subscribed_events JSONB DEFAULT '["open", "click", "bounce", "delivered"]',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- Phase 9: Deliverability & IP Pooling
CREATE TABLE ip_pools (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID REFERENCES organizations(id) ON DELETE CASCADE, -- If null, it's a system-wide shared pool
    name VARCHAR(100) NOT NULL,
    type VARCHAR(20) DEFAULT 'shared' -- 'shared' or 'dedicated'
);

CREATE TABLE ip_addresses (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    ip_pool_id UUID REFERENCES ip_pools(id) ON DELETE CASCADE,
    ip_address VARCHAR(45) UNIQUE NOT NULL, -- Supports IPv4 and IPv6
    is_active BOOLEAN DEFAULT TRUE,
    reputation_score INT DEFAULT 100 -- 0 to 100
);

ALTER TABLE sub_accounts ADD COLUMN ip_pool_id UUID REFERENCES ip_pools(id) ON DELETE SET NULL;

-- Phase 10: Advanced Security (DKIM & SPF)
ALTER TABLE domains ADD COLUMN dkim_private_key TEXT;
ALTER TABLE domains ADD COLUMN dkim_public_key TEXT;
ALTER TABLE domains ADD COLUMN dkim_selector VARCHAR(50) DEFAULT 'esp';
ALTER TABLE domains ADD COLUMN spf_verified BOOLEAN DEFAULT FALSE;
ALTER TABLE domains ADD COLUMN dkim_verified BOOLEAN DEFAULT FALSE;

-- Phase 11: A/B Testing & Campaigns
CREATE TABLE mailing_lists (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID REFERENCES organizations(id) ON DELETE CASCADE,
    name VARCHAR(255) NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE contacts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    list_id UUID REFERENCES mailing_lists(id) ON DELETE CASCADE,
    email VARCHAR(255) NOT NULL,
    first_name VARCHAR(100),
    last_name VARCHAR(100),
    metadata JSONB,
    UNIQUE(list_id, email)
);

CREATE TABLE campaigns (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID REFERENCES organizations(id) ON DELETE CASCADE,
    list_id UUID REFERENCES mailing_lists(id),
    name VARCHAR(255) NOT NULL,
    status VARCHAR(50) DEFAULT 'draft', -- draft, sending, completed
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE campaign_variants (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    campaign_id UUID REFERENCES campaigns(id) ON DELETE CASCADE,
    template_id UUID REFERENCES templates(id),
    weight_percentage INT NOT NULL -- e.g., 50 for 50% of the list
);

-- Phase 12: Advanced Mailbox UX
CREATE TABLE labels (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    mailbox_id UUID REFERENCES mailboxes(id) ON DELETE CASCADE,
    name VARCHAR(100) NOT NULL,
    color VARCHAR(7) DEFAULT '#888888',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(mailbox_id, name)
);

CREATE TABLE message_labels (
    message_id UUID REFERENCES messages(id) ON DELETE CASCADE,
    label_id UUID REFERENCES labels(id) ON DELETE CASCADE,
    PRIMARY KEY (message_id, label_id)
);

CREATE TABLE mailbox_delegates (
    mailbox_id UUID REFERENCES mailboxes(id) ON DELETE CASCADE,
    delegate_user_id UUID REFERENCES users(id) ON DELETE CASCADE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (mailbox_id, delegate_user_id)
);

CREATE TABLE scheduled_emails (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    message_id UUID REFERENCES messages(id) ON DELETE CASCADE,
    send_at TIMESTAMP WITH TIME ZONE NOT NULL,
    status VARCHAR(50) DEFAULT 'pending', -- pending, sent, canceled
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

ALTER TABLE messages ADD COLUMN thread_id VARCHAR(255);
ALTER TABLE messages ADD COLUMN snoozed_until TIMESTAMP WITH TIME ZONE;
ALTER TABLE messages ADD COLUMN category VARCHAR(50) DEFAULT 'primary'; -- primary, social, promotions, updates, forums
ALTER TABLE messages ADD COLUMN is_confidential BOOLEAN DEFAULT FALSE;
ALTER TABLE messages ADD COLUMN confidential_expires_at TIMESTAMP WITH TIME ZONE;

-- Phase 13: Organization Admin & Compliance
CREATE TABLE aliases (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    mailbox_id UUID REFERENCES mailboxes(id) ON DELETE CASCADE,
    email_address VARCHAR(255) UNIQUE NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE routing_rules (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID REFERENCES organizations(id) ON DELETE CASCADE,
    condition_pattern VARCHAR(255) NOT NULL, -- e.g., 'catch-all', 'regex:*'
    action_type VARCHAR(50) NOT NULL, -- 'forward', 'reject', 'label'
    action_target VARCHAR(255),
    priority INT DEFAULT 0
);

CREATE TABLE dlp_policies (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID REFERENCES organizations(id) ON DELETE CASCADE,
    name VARCHAR(255) NOT NULL,
    regex_pattern TEXT NOT NULL,
    action VARCHAR(50) DEFAULT 'block' -- block, quarantine, warn
);

CREATE TABLE vault_archives (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID REFERENCES organizations(id) ON DELETE CASCADE,
    original_message_id UUID,
    raw_eml TEXT NOT NULL,
    archived_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE auto_responders (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    mailbox_id UUID REFERENCES mailboxes(id) ON DELETE CASCADE,
    is_active BOOLEAN DEFAULT FALSE,
    start_date TIMESTAMP WITH TIME ZONE,
    end_date TIMESTAMP WITH TIME ZONE,
    subject TEXT,
    body TEXT
);

-- Phase 14: Security & Quotas
CREATE TABLE dmarc_reports (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    domain_id UUID REFERENCES domains(id) ON DELETE CASCADE,
    report_date DATE NOT NULL,
    xml_data TEXT NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE rate_limits (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    sub_account_id UUID REFERENCES sub_accounts(id) ON DELETE CASCADE,
    hourly_limit INT NOT NULL DEFAULT 1000,
    daily_limit INT NOT NULL DEFAULT 10000
);

ALTER TABLE messages ADD COLUMN spam_score FLOAT;
ALTER TABLE messages ADD COLUMN phishing_flag BOOLEAN DEFAULT FALSE;

-- Phase 15: Integrations
ALTER TABLE attachments ADD COLUMN cloud_storage_url VARCHAR(500);
ALTER TABLE attachments ADD COLUMN is_inline BOOLEAN DEFAULT FALSE;

-- Phase 16: Advanced Mailbox UX (Outlook Style)
ALTER TABLE messages ADD COLUMN is_pinned BOOLEAN DEFAULT FALSE;
ALTER TABLE mailboxes ADD COLUMN mailbox_type VARCHAR(50) DEFAULT 'user'; -- 'user', 'shared', 'group'

CREATE TABLE ignored_threads (
    mailbox_id UUID REFERENCES mailboxes(id) ON DELETE CASCADE,
    thread_id VARCHAR(255) NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (mailbox_id, thread_id)
);

CREATE TABLE sweep_rules (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    mailbox_id UUID REFERENCES mailboxes(id) ON DELETE CASCADE,
    sender VARCHAR(255) NOT NULL,
    action VARCHAR(50) NOT NULL, -- 'delete_all', 'keep_latest', 'move_to_folder'
    days_older_than INT DEFAULT 0,
    target_folder_id UUID REFERENCES folders(id) ON DELETE CASCADE
);

-- Phase 17: Compliance & Admin (Exchange Style)
ALTER TABLE mailboxes ADD COLUMN litigation_hold BOOLEAN DEFAULT FALSE;

CREATE TABLE information_barriers (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID REFERENCES organizations(id) ON DELETE CASCADE,
    segment_a VARCHAR(255) NOT NULL, -- e.g. domain, or specific group
    segment_b VARCHAR(255) NOT NULL,
    block_direction VARCHAR(10) DEFAULT 'both' -- 'both', 'a_to_b', 'b_to_a'
);

-- Phase 18: Advanced Security (Defender Style)
ALTER TABLE attachments ADD COLUMN attachment_scan_status VARCHAR(50) DEFAULT 'pending'; -- pending, clean, malicious

CREATE TABLE message_traces (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    message_id VARCHAR(255) NOT NULL,
    org_id UUID REFERENCES organizations(id) ON DELETE CASCADE,
    event_time TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    event_type VARCHAR(100) NOT NULL, -- received, routed, rewritten, delivered, zap
    details JSONB
);

-- Phase 19: Enhancements & Integrations
CREATE TABLE meeting_polls (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    message_id UUID REFERENCES messages(id) ON DELETE CASCADE,
    poll_data JSONB
);

-- ==========================================
-- Phase 24: Workspace Ecosystem Expansion
-- ==========================================

-- 1. Real-Time Collaboration (Docs)
CREATE TABLE documents (
    	id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
	org_id UUID REFERENCES sub_accounts(id) ON DELETE CASCADE,
	title VARCHAR(255) NOT NULL,
	content TEXT,
	crdt_state TEXT, -- Phase 26: Yjs binary state encoded as Base64
	version INTEGER DEFAULT 1,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- 2. Cloud Storage (Drive)
CREATE TABLE drive_files (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID REFERENCES organizations(id) ON DELETE CASCADE,
    owner_id UUID REFERENCES sub_accounts(id) ON DELETE CASCADE,
    filename VARCHAR(255) NOT NULL,
    size_bytes BIGINT NOT NULL,
    mime_type VARCHAR(100),
    storage_url VARCHAR(255) NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- 3. Calendars (Shared Calendars)
CREATE TABLE calendar_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID REFERENCES organizations(id) ON DELETE CASCADE,
    organizer_id UUID REFERENCES sub_accounts(id) ON DELETE CASCADE,
    title VARCHAR(255) NOT NULL,
    description TEXT,
    start_time TIMESTAMP WITH TIME ZONE NOT NULL,
    end_time TIMESTAMP WITH TIME ZONE NOT NULL,
    attendees JSONB, -- list of emails
    meeting_link VARCHAR(255),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- ==========================================
-- Phase 31: Rules Engine & Context-Aware Access
-- ==========================================

CREATE TABLE activity_rules (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID REFERENCES organizations(id) ON DELETE CASCADE,
    name VARCHAR(255) NOT NULL,
    condition_attribute VARCHAR(50) NOT NULL, -- e.g., 'IP_ADDRESS'
    condition_operator VARCHAR(50) NOT NULL, -- e.g., 'NOT_IN_CIDR'
    condition_value TEXT NOT NULL, -- e.g., '198.51.100.0/24'
    action VARCHAR(50) NOT NULL, -- e.g., 'SUSPEND_USER'
    is_active BOOLEAN DEFAULT TRUE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE caa_policies (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID REFERENCES organizations(id) ON DELETE CASCADE,
    app_name VARCHAR(100) NOT NULL, -- e.g., 'Google Drive', 'Gmail'
    cel_expression TEXT, -- Phase 32: Advanced CEL Logic (Device + IP)
    is_active BOOLEAN DEFAULT TRUE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- ==========================================
-- Phase 44: Automation & Journey Builder
-- ==========================================

CREATE TABLE journeys (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID REFERENCES organizations(id) ON DELETE CASCADE,
    name VARCHAR(255) NOT NULL,
    status VARCHAR(50) DEFAULT 'DRAFT', -- DRAFT, ACTIVE, PAUSED
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE journey_nodes (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    journey_id UUID REFERENCES journeys(id) ON DELETE CASCADE,
    node_type VARCHAR(50) NOT NULL, -- TRIGGER, DELAY, ACTION_SEND_EMAIL, CONDITION_OPENED
    config JSONB NOT NULL DEFAULT '{}'::jsonb, -- e.g., {"delay_hours": 48} or {"template_id": "..."}
    next_node_id UUID, -- Standard path
    false_next_node_id UUID, -- For CONDITIONS
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE journey_contacts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    journey_id UUID REFERENCES journeys(id) ON DELETE CASCADE,
    email VARCHAR(255) NOT NULL,
    current_node_id UUID REFERENCES journey_nodes(id) ON DELETE SET NULL,
    status VARCHAR(50) DEFAULT 'IN_PROGRESS', -- IN_PROGRESS, COMPLETED, FAILED
    next_execute_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(journey_id, email)
);

-- ==========================================
-- Phase 45: A/B Split Testing & Multivariate
-- ==========================================

CREATE TABLE ab_tests (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID REFERENCES organizations(id) ON DELETE CASCADE,
    campaign_id UUID NOT NULL, -- Logical link to the parent campaign
    status VARCHAR(50) DEFAULT 'RUNNING', -- RUNNING, CONCLUDED
    test_end_time TIMESTAMP WITH TIME ZONE NOT NULL,
    winner_variant_id UUID,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE ab_test_variants (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    ab_test_id UUID REFERENCES ab_tests(id) ON DELETE CASCADE,
    variant_name VARCHAR(50) NOT NULL, -- e.g., 'A', 'B'
    subject_line VARCHAR(255),
    template_id UUID,
    sent_count INT DEFAULT 0,
    open_count INT DEFAULT 0,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- ==========================================
-- Phase 46: Dedicated IP Warmup Engine
-- ==========================================

CREATE TABLE dedicated_ips (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID REFERENCES organizations(id) ON DELETE CASCADE,
    ip_address VARCHAR(45) UNIQUE NOT NULL,
    status VARCHAR(50) DEFAULT 'WARMING_UP', -- WARMING_UP, ACTIVE, SUSPENDED
    warmup_day INT DEFAULT 1,
    current_daily_limit INT DEFAULT 50,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- ==========================================
-- Phase 47: Billing & Metering Engine
-- ==========================================

CREATE TABLE billing_subscriptions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID REFERENCES organizations(id) ON DELETE CASCADE UNIQUE,
    stripe_customer_id VARCHAR(100),
    tier VARCHAR(50) DEFAULT 'FREE', -- FREE, PRO, ENTERPRISE
    status VARCHAR(50) DEFAULT 'ACTIVE', -- ACTIVE, PAST_DUE, CANCELED
    current_period_end TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE billing_invoices (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID REFERENCES organizations(id) ON DELETE CASCADE,
    stripe_invoice_id VARCHAR(100) UNIQUE,
    amount_due INT, -- In cents
    status VARCHAR(50), -- OPEN, PAID, UNCOLLECTIBLE
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- ==========================================
-- Phase 49: Outbound Event Firehose
-- ==========================================

CREATE TABLE customer_webhooks (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID REFERENCES organizations(id) ON DELETE CASCADE,
    target_url VARCHAR(500) NOT NULL,
    subscribed_events JSONB DEFAULT '["OPEN", "CLICK", "BOUNCE", "COMPLAINT"]',
    is_active BOOLEAN DEFAULT TRUE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);
