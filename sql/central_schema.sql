-- =============================================
-- PULL PLATFORM - CENTRAL DATABASE SCHEMA
-- =============================================
-- Este schema es para la base de datos CENTRAL de Pull
-- Contiene: venues registry, configs, platform transactions, pull staff
--
-- Ejecutar en: https://supabase.com/dashboard/project/dqqvtehpidihahzabcxg
-- SQL Editor -> New Query -> Paste -> Run
-- =============================================

-- Enable extensions
CREATE EXTENSION IF NOT EXISTS "pgcrypto";
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

-- =============================================
-- ENUMS
-- =============================================

DO $$ BEGIN
    CREATE TYPE subscription_status AS ENUM ('trial', 'active', 'suspended', 'cancelled', 'past_due');
EXCEPTION
    WHEN duplicate_object THEN null;
END $$;

DO $$ BEGIN
    CREATE TYPE subscription_plan AS ENUM ('free', 'basic', 'pro', 'enterprise');
EXCEPTION
    WHEN duplicate_object THEN null;
END $$;

DO $$ BEGIN
    CREATE TYPE payment_gateway_type AS ENUM ('stripe', 'neonet', 'mercadopago', 'paypal');
EXCEPTION
    WHEN duplicate_object THEN null;
END $$;

DO $$ BEGIN
    CREATE TYPE platform_staff_role AS ENUM ('viewer', 'analyst', 'support', 'admin', 'super_admin');
EXCEPTION
    WHEN duplicate_object THEN null;
END $$;

DO $$ BEGIN
    CREATE TYPE transaction_status AS ENUM ('pending', 'completed', 'failed', 'refunded', 'partially_refunded');
EXCEPTION
    WHEN duplicate_object THEN null;
END $$;

DO $$ BEGIN
    CREATE TYPE support_status AS ENUM ('open', 'in_progress', 'waiting_customer', 'resolved', 'closed');
EXCEPTION
    WHEN duplicate_object THEN null;
END $$;

-- =============================================
-- ORGANIZATIONS
-- =============================================

CREATE TABLE IF NOT EXISTS organizations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL,
    slug TEXT UNIQUE NOT NULL,
    legal_name TEXT,
    tax_id TEXT,

    -- Contact
    contact_email TEXT,
    contact_phone TEXT,

    -- Billing
    billing_email TEXT,
    billing_address TEXT,
    billing_city TEXT,
    billing_country TEXT DEFAULT 'GT',

    -- Settings
    default_currency TEXT DEFAULT 'GTQ',
    timezone TEXT DEFAULT 'America/Guatemala',

    -- Status
    is_active BOOLEAN DEFAULT true,

    -- Timestamps
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW(),
    deleted_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_organizations_slug ON organizations(slug);
CREATE INDEX IF NOT EXISTS idx_organizations_active ON organizations(is_active) WHERE is_active = true;

-- =============================================
-- VENUES (Registry only - data lives in venue DBs)
-- =============================================

CREATE TABLE IF NOT EXISTS venues (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,

    -- Basic Info
    name TEXT NOT NULL,
    slug TEXT UNIQUE NOT NULL,
    description TEXT,
    image TEXT,

    -- Location
    location TEXT,
    city TEXT,
    country TEXT DEFAULT 'GT',
    latitude DECIMAL(10, 8),
    longitude DECIMAL(11, 8),

    -- Contact
    contact_email TEXT,
    contact_phone TEXT,
    whatsapp_number TEXT,

    -- Settings
    currency TEXT DEFAULT 'GTQ',
    timezone TEXT DEFAULT 'America/Guatemala',

    -- Platform Fees (configurable per venue)
    platform_fee_percent DECIMAL(5, 2) DEFAULT 5.00,  -- 5%
    platform_fee_fixed DECIMAL(10, 2) DEFAULT 0.00,   -- Fixed fee per transaction

    -- Subscription
    subscription_status subscription_status DEFAULT 'trial',
    subscription_plan subscription_plan DEFAULT 'basic',
    subscription_started_at TIMESTAMPTZ,
    subscription_expires_at TIMESTAMPTZ,
    trial_ends_at TIMESTAMPTZ DEFAULT (NOW() + INTERVAL '14 days'),

    -- Payment Gateway (default for this venue)
    primary_payment_gateway payment_gateway_type DEFAULT 'stripe',

    -- Features
    use_vip_list_flow BOOLEAN DEFAULT false,
    use_guest_list BOOLEAN DEFAULT true,
    use_group_reservations BOOLEAN DEFAULT true,

    -- Status
    is_active BOOLEAN DEFAULT true,
    is_verified BOOLEAN DEFAULT false,
    verified_at TIMESTAMPTZ,

    -- Timestamps
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW(),
    deleted_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_venues_slug ON venues(slug);
CREATE INDEX IF NOT EXISTS idx_venues_organization ON venues(organization_id);
CREATE INDEX IF NOT EXISTS idx_venues_active ON venues(is_active) WHERE is_active = true AND deleted_at IS NULL;

-- =============================================
-- VENUE DATABASE CONFIGS (Connection to venue Supabase)
-- =============================================

CREATE TABLE IF NOT EXISTS venue_database_configs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    venue_id UUID NOT NULL REFERENCES venues(id) ON DELETE CASCADE,

    -- Supabase Connection (credentials should be encrypted at app level)
    supabase_url TEXT NOT NULL,
    supabase_project_id TEXT,
    service_key TEXT NOT NULL,      -- Encrypted with AES-256-GCM
    anon_key TEXT NOT NULL,         -- Encrypted with AES-256-GCM

    -- Connection Settings
    max_connections INT DEFAULT 50,
    connection_timeout INT DEFAULT 10,  -- seconds

    -- Status
    is_active BOOLEAN DEFAULT true,
    last_health_check TIMESTAMPTZ,
    health_status TEXT DEFAULT 'unknown',  -- 'healthy', 'unhealthy', 'unknown'

    -- Timestamps
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW(),

    UNIQUE(venue_id)
);

CREATE INDEX IF NOT EXISTS idx_venue_db_configs_venue ON venue_database_configs(venue_id);

-- =============================================
-- VENUE PAYMENT CONFIGS (Gateway credentials per venue)
-- =============================================

CREATE TABLE IF NOT EXISTS venue_payment_configs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    venue_id UUID NOT NULL REFERENCES venues(id) ON DELETE CASCADE,

    -- Gateway Type
    gateway payment_gateway_type NOT NULL,
    is_primary BOOLEAN DEFAULT false,
    is_active BOOLEAN DEFAULT true,

    -- Credentials (encrypted JSON with AES-256-GCM)
    -- Structure varies by gateway:
    -- Stripe: {"secret_key": "sk_...", "publishable_key": "pk_...", "webhook_secret": "whsec_..."}
    -- NeoNet: {"profile_id": "...", "access_key": "...", "secret_key": "...", "merchant_id": "...", "terminal_id": "...", "environment": "test|production"}
    -- MercadoPago: {"access_token": "...", "public_key": "..."}
    credentials_encrypted TEXT NOT NULL,

    -- Gateway-specific settings (not sensitive)
    settings JSONB DEFAULT '{}',

    -- For Stripe Connect
    stripe_account_id TEXT,  -- Connected account ID
    stripe_account_type TEXT,  -- 'standard', 'express', 'custom'

    -- Timestamps
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW(),

    UNIQUE(venue_id, gateway)
);

CREATE INDEX IF NOT EXISTS idx_venue_payment_configs_venue ON venue_payment_configs(venue_id);
CREATE INDEX IF NOT EXISTS idx_venue_payment_configs_primary ON venue_payment_configs(venue_id, is_primary) WHERE is_primary = true;

-- =============================================
-- PLATFORM TRANSACTIONS (All transactions for revenue tracking)
-- =============================================

CREATE TABLE IF NOT EXISTS platform_transactions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    -- References
    venue_id UUID NOT NULL REFERENCES venues(id),
    organization_id UUID NOT NULL REFERENCES organizations(id),

    -- Transaction Info
    transaction_type TEXT NOT NULL,  -- 'ticket_sale', 'group_organizer', 'group_guest', 'vip_list', 'refund'

    -- Amounts
    gross_amount DECIMAL(12, 2) NOT NULL,
    platform_fee_percent DECIMAL(5, 2) NOT NULL,
    platform_fee_fixed DECIMAL(10, 2) NOT NULL DEFAULT 0,
    platform_fee_total DECIMAL(12, 2) NOT NULL,
    gateway_fee DECIMAL(12, 2) DEFAULT 0,
    venue_net_amount DECIMAL(12, 2) NOT NULL,

    -- Currency
    currency TEXT NOT NULL DEFAULT 'GTQ',

    -- Payment Info
    payment_gateway payment_gateway_type NOT NULL,
    gateway_transaction_id TEXT,  -- External ID from Stripe/NeoNet/etc
    gateway_response JSONB,       -- Raw response for debugging

    -- Reference to venue's order/reservation
    venue_order_id TEXT,          -- Order ID in venue's database
    venue_reservation_id TEXT,    -- Reservation ID if applicable

    -- Status
    status transaction_status DEFAULT 'pending',

    -- Refund tracking
    original_transaction_id UUID REFERENCES platform_transactions(id),
    refund_reason TEXT,

    -- Customer Info (for analytics, not PII)
    customer_country TEXT,

    -- Timestamps
    processed_at TIMESTAMPTZ DEFAULT NOW(),
    created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_platform_tx_venue ON platform_transactions(venue_id);
CREATE INDEX IF NOT EXISTS idx_platform_tx_org ON platform_transactions(organization_id);
CREATE INDEX IF NOT EXISTS idx_platform_tx_date ON platform_transactions(processed_at);
CREATE INDEX IF NOT EXISTS idx_platform_tx_status ON platform_transactions(status);
CREATE INDEX IF NOT EXISTS idx_platform_tx_type ON platform_transactions(transaction_type);
CREATE INDEX IF NOT EXISTS idx_platform_tx_gateway ON platform_transactions(payment_gateway);

-- =============================================
-- PLATFORM DAILY REVENUE (Aggregated daily stats)
-- =============================================

CREATE TABLE IF NOT EXISTS platform_daily_revenue (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    date DATE NOT NULL UNIQUE,

    -- Totals
    total_transactions INT DEFAULT 0,
    total_gross_amount DECIMAL(14, 2) DEFAULT 0,
    total_platform_fee DECIMAL(14, 2) DEFAULT 0,
    total_gateway_fee DECIMAL(14, 2) DEFAULT 0,
    total_net_to_venues DECIMAL(14, 2) DEFAULT 0,

    -- By Transaction Type
    ticket_sales_count INT DEFAULT 0,
    ticket_sales_gross DECIMAL(14, 2) DEFAULT 0,
    ticket_sales_platform_fee DECIMAL(14, 2) DEFAULT 0,

    group_sales_count INT DEFAULT 0,
    group_sales_gross DECIMAL(14, 2) DEFAULT 0,
    group_sales_platform_fee DECIMAL(14, 2) DEFAULT 0,

    vip_list_count INT DEFAULT 0,
    vip_list_gross DECIMAL(14, 2) DEFAULT 0,
    vip_list_platform_fee DECIMAL(14, 2) DEFAULT 0,

    refund_count INT DEFAULT 0,
    refund_amount DECIMAL(14, 2) DEFAULT 0,

    -- Activity
    active_venues INT DEFAULT 0,
    active_events INT DEFAULT 0,
    unique_customers INT DEFAULT 0,
    new_customers INT DEFAULT 0,

    -- Tickets
    total_tickets_sold INT DEFAULT 0,

    -- By Gateway
    stripe_transactions INT DEFAULT 0,
    stripe_gross DECIMAL(14, 2) DEFAULT 0,
    neonet_transactions INT DEFAULT 0,
    neonet_gross DECIMAL(14, 2) DEFAULT 0,
    mercadopago_transactions INT DEFAULT 0,
    mercadopago_gross DECIMAL(14, 2) DEFAULT 0,

    -- Timestamps
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_platform_daily_date ON platform_daily_revenue(date);

-- =============================================
-- PLATFORM MONTHLY REVENUE (Aggregated monthly stats)
-- =============================================

CREATE TABLE IF NOT EXISTS platform_monthly_revenue (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    year INT NOT NULL,
    month INT NOT NULL CHECK (month >= 1 AND month <= 12),

    -- Totals
    total_transactions INT DEFAULT 0,
    total_gross_amount DECIMAL(16, 2) DEFAULT 0,
    total_platform_fee DECIMAL(16, 2) DEFAULT 0,
    total_gateway_fee DECIMAL(16, 2) DEFAULT 0,
    total_net_to_venues DECIMAL(16, 2) DEFAULT 0,

    -- By Type
    ticket_sales_count INT DEFAULT 0,
    ticket_sales_gross DECIMAL(16, 2) DEFAULT 0,

    group_sales_count INT DEFAULT 0,
    group_sales_gross DECIMAL(16, 2) DEFAULT 0,

    vip_list_count INT DEFAULT 0,
    vip_list_gross DECIMAL(16, 2) DEFAULT 0,

    refund_count INT DEFAULT 0,
    refund_amount DECIMAL(16, 2) DEFAULT 0,

    -- Activity
    active_venues INT DEFAULT 0,
    total_events INT DEFAULT 0,
    unique_customers INT DEFAULT 0,
    new_customers INT DEFAULT 0,
    total_tickets_sold INT DEFAULT 0,

    -- Growth
    gross_change_vs_prev_month DECIMAL(8, 2),
    platform_fee_change_vs_prev_month DECIMAL(8, 2),

    -- Timestamps
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW(),

    UNIQUE(year, month)
);

CREATE INDEX IF NOT EXISTS idx_platform_monthly_year_month ON platform_monthly_revenue(year, month);

-- =============================================
-- PULL STAFF (Platform administrators)
-- =============================================

CREATE TABLE IF NOT EXISTS pull_staff (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    -- Auth
    email TEXT NOT NULL UNIQUE CHECK (email ~* '^[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}$'),
    password_hash TEXT NOT NULL,

    -- Profile
    name TEXT NOT NULL,
    avatar_url TEXT,

    -- Role & Permissions
    role platform_staff_role NOT NULL DEFAULT 'viewer',
    permissions JSONB DEFAULT '[]',  -- Additional granular permissions

    -- Security
    is_active BOOLEAN DEFAULT true,
    must_change_password BOOLEAN DEFAULT false,
    password_changed_at TIMESTAMPTZ DEFAULT NOW(),
    failed_login_attempts INT DEFAULT 0,
    locked_until TIMESTAMPTZ,

    -- 2FA
    two_factor_enabled BOOLEAN DEFAULT false,
    two_factor_secret TEXT,

    -- Activity
    last_login_at TIMESTAMPTZ,
    last_login_ip TEXT,

    -- Timestamps
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_pull_staff_email ON pull_staff(email);
CREATE INDEX IF NOT EXISTS idx_pull_staff_active ON pull_staff(is_active) WHERE is_active = true;

-- =============================================
-- PULL STAFF SESSIONS
-- =============================================

CREATE TABLE IF NOT EXISTS pull_staff_sessions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    staff_id UUID NOT NULL REFERENCES pull_staff(id) ON DELETE CASCADE,

    -- Token
    token_hash TEXT NOT NULL,  -- SHA-256 hash of JWT
    jti TEXT UNIQUE,           -- JWT ID for revocation

    -- Metadata
    ip_address TEXT,
    user_agent TEXT,
    device_info TEXT,

    -- Expiry
    expires_at TIMESTAMPTZ NOT NULL,

    -- Revocation
    is_revoked BOOLEAN DEFAULT false,
    revoked_at TIMESTAMPTZ,
    revoked_reason TEXT,

    -- Timestamps
    created_at TIMESTAMPTZ DEFAULT NOW(),
    last_used_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_pull_staff_sessions_staff ON pull_staff_sessions(staff_id);
CREATE INDEX IF NOT EXISTS idx_pull_staff_sessions_jti ON pull_staff_sessions(jti);
CREATE INDEX IF NOT EXISTS idx_pull_staff_sessions_active ON pull_staff_sessions(staff_id, is_revoked) WHERE is_revoked = false;

-- =============================================
-- PULL STAFF AUDIT LOG
-- =============================================

CREATE TABLE IF NOT EXISTS pull_staff_audit_log (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    staff_id UUID REFERENCES pull_staff(id) ON DELETE SET NULL,

    -- Action
    action TEXT NOT NULL,  -- 'login', 'logout', 'view_venue', 'update_venue', 'view_transactions', etc.
    resource_type TEXT,    -- 'venue', 'organization', 'transaction', etc.
    resource_id UUID,

    -- Details
    details JSONB DEFAULT '{}',

    -- Request Info
    ip_address TEXT,
    user_agent TEXT,

    -- Timestamp
    created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_pull_audit_staff ON pull_staff_audit_log(staff_id);
CREATE INDEX IF NOT EXISTS idx_pull_audit_action ON pull_staff_audit_log(action);
CREATE INDEX IF NOT EXISTS idx_pull_audit_date ON pull_staff_audit_log(created_at);

-- =============================================
-- SUPPORT REQUESTS
-- =============================================

CREATE TABLE IF NOT EXISTS support_requests (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    -- Requester (can be venue staff or public user)
    requester_type TEXT NOT NULL,  -- 'venue_staff', 'public_user', 'anonymous'
    requester_email TEXT NOT NULL,
    requester_name TEXT,
    venue_id UUID REFERENCES venues(id),

    -- Request
    subject TEXT NOT NULL CHECK (char_length(subject) >= 3 AND char_length(subject) <= 200),
    message TEXT NOT NULL CHECK (char_length(message) >= 10),
    category TEXT DEFAULT 'general',  -- 'general', 'billing', 'technical', 'feature_request', 'complaint'
    priority INT DEFAULT 3 CHECK (priority >= 1 AND priority <= 5),  -- 1=highest, 5=lowest

    -- Status
    status support_status DEFAULT 'open',

    -- Assignment
    assigned_to UUID REFERENCES pull_staff(id),
    assigned_at TIMESTAMPTZ,

    -- Resolution
    resolved_by UUID REFERENCES pull_staff(id),
    resolved_at TIMESTAMPTZ,
    resolution_notes TEXT,

    -- Metadata
    source TEXT DEFAULT 'web',  -- 'web', 'mobile', 'email', 'phone'
    client_ip TEXT,
    user_agent TEXT,
    metadata JSONB DEFAULT '{}',

    -- SLA
    first_response_at TIMESTAMPTZ,

    -- Timestamps
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_support_status ON support_requests(status);
CREATE INDEX IF NOT EXISTS idx_support_venue ON support_requests(venue_id);
CREATE INDEX IF NOT EXISTS idx_support_assigned ON support_requests(assigned_to);
CREATE INDEX IF NOT EXISTS idx_support_date ON support_requests(created_at);

-- =============================================
-- SUPPORT REQUEST MESSAGES (Thread)
-- =============================================

CREATE TABLE IF NOT EXISTS support_request_messages (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    request_id UUID NOT NULL REFERENCES support_requests(id) ON DELETE CASCADE,

    -- Sender
    sender_type TEXT NOT NULL,  -- 'customer', 'staff'
    sender_id UUID,             -- pull_staff.id if staff
    sender_name TEXT,
    sender_email TEXT,

    -- Message
    message TEXT NOT NULL,

    -- Attachments
    attachments JSONB DEFAULT '[]',  -- [{url, filename, size, type}]

    -- Internal
    is_internal BOOLEAN DEFAULT false,  -- Internal note, not visible to customer

    -- Timestamps
    created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_support_messages_request ON support_request_messages(request_id);

-- =============================================
-- WEBHOOKS LOG (For debugging payment webhooks)
-- =============================================

CREATE TABLE IF NOT EXISTS webhook_logs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    -- Source
    gateway payment_gateway_type NOT NULL,
    venue_id UUID REFERENCES venues(id),

    -- Request
    endpoint TEXT NOT NULL,
    method TEXT NOT NULL,
    headers JSONB,
    body TEXT,

    -- Processing
    event_type TEXT,
    event_id TEXT,

    -- Response
    status_code INT,
    response_body TEXT,

    -- Result
    processed BOOLEAN DEFAULT false,
    error_message TEXT,

    -- Timestamps
    received_at TIMESTAMPTZ DEFAULT NOW(),
    processed_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_webhook_logs_gateway ON webhook_logs(gateway);
CREATE INDEX IF NOT EXISTS idx_webhook_logs_venue ON webhook_logs(venue_id);
CREATE INDEX IF NOT EXISTS idx_webhook_logs_date ON webhook_logs(received_at);

-- =============================================
-- ROW LEVEL SECURITY (RLS)
-- =============================================

ALTER TABLE organizations ENABLE ROW LEVEL SECURITY;
ALTER TABLE venues ENABLE ROW LEVEL SECURITY;
ALTER TABLE venue_database_configs ENABLE ROW LEVEL SECURITY;
ALTER TABLE venue_payment_configs ENABLE ROW LEVEL SECURITY;
ALTER TABLE platform_transactions ENABLE ROW LEVEL SECURITY;
ALTER TABLE platform_daily_revenue ENABLE ROW LEVEL SECURITY;
ALTER TABLE platform_monthly_revenue ENABLE ROW LEVEL SECURITY;
ALTER TABLE pull_staff ENABLE ROW LEVEL SECURITY;
ALTER TABLE pull_staff_sessions ENABLE ROW LEVEL SECURITY;
ALTER TABLE pull_staff_audit_log ENABLE ROW LEVEL SECURITY;
ALTER TABLE support_requests ENABLE ROW LEVEL SECURITY;
ALTER TABLE support_request_messages ENABLE ROW LEVEL SECURITY;
ALTER TABLE webhook_logs ENABLE ROW LEVEL SECURITY;

-- Service role has full access (used by API with service_key)
CREATE POLICY "Service role full access" ON organizations FOR ALL USING (true) WITH CHECK (true);
CREATE POLICY "Service role full access" ON venues FOR ALL USING (true) WITH CHECK (true);
CREATE POLICY "Service role full access" ON venue_database_configs FOR ALL USING (true) WITH CHECK (true);
CREATE POLICY "Service role full access" ON venue_payment_configs FOR ALL USING (true) WITH CHECK (true);
CREATE POLICY "Service role full access" ON platform_transactions FOR ALL USING (true) WITH CHECK (true);
CREATE POLICY "Service role full access" ON platform_daily_revenue FOR ALL USING (true) WITH CHECK (true);
CREATE POLICY "Service role full access" ON platform_monthly_revenue FOR ALL USING (true) WITH CHECK (true);
CREATE POLICY "Service role full access" ON pull_staff FOR ALL USING (true) WITH CHECK (true);
CREATE POLICY "Service role full access" ON pull_staff_sessions FOR ALL USING (true) WITH CHECK (true);
CREATE POLICY "Service role full access" ON pull_staff_audit_log FOR ALL USING (true) WITH CHECK (true);
CREATE POLICY "Service role full access" ON support_requests FOR ALL USING (true) WITH CHECK (true);
CREATE POLICY "Service role full access" ON support_request_messages FOR ALL USING (true) WITH CHECK (true);
CREATE POLICY "Service role full access" ON webhook_logs FOR ALL USING (true) WITH CHECK (true);

-- =============================================
-- FUNCTIONS
-- =============================================

-- Function to update updated_at timestamp
CREATE OR REPLACE FUNCTION update_updated_at_column()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ language 'plpgsql';

-- Apply trigger to tables with updated_at
CREATE TRIGGER update_organizations_updated_at BEFORE UPDATE ON organizations FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
CREATE TRIGGER update_venues_updated_at BEFORE UPDATE ON venues FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
CREATE TRIGGER update_venue_database_configs_updated_at BEFORE UPDATE ON venue_database_configs FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
CREATE TRIGGER update_venue_payment_configs_updated_at BEFORE UPDATE ON venue_payment_configs FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
CREATE TRIGGER update_platform_daily_revenue_updated_at BEFORE UPDATE ON platform_daily_revenue FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
CREATE TRIGGER update_platform_monthly_revenue_updated_at BEFORE UPDATE ON platform_monthly_revenue FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
CREATE TRIGGER update_pull_staff_updated_at BEFORE UPDATE ON pull_staff FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
CREATE TRIGGER update_support_requests_updated_at BEFORE UPDATE ON support_requests FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

-- =============================================
-- SEED DATA (Optional - for testing)
-- =============================================

-- Create a test organization
INSERT INTO organizations (id, name, slug, contact_email, is_active)
VALUES (
    'a0000000-0000-0000-0000-000000000001',
    'Test Organization',
    'test-org',
    'test@example.com',
    true
) ON CONFLICT (slug) DO NOTHING;

-- Create a test venue
INSERT INTO venues (id, organization_id, name, slug, platform_fee_percent, platform_fee_fixed, is_active)
VALUES (
    'b0000000-0000-0000-0000-000000000001',
    'a0000000-0000-0000-0000-000000000001',
    'Test Venue',
    'test-venue',
    5.00,
    0.50,
    true
) ON CONFLICT (slug) DO NOTHING;

-- Create a test platform admin
-- Password: admin123 (bcrypt hash)
INSERT INTO pull_staff (id, email, password_hash, name, role, is_active)
VALUES (
    'c0000000-0000-0000-0000-000000000001',
    'admin@pullevents.com',
    '$2a$10$rQEY7kLSzKjJ0nqjkPmXPOyVL.8R1v1vBV6mN8nV4Y7L3K8VQ2Kie',  -- admin123
    'Platform Admin',
    'super_admin',
    true
) ON CONFLICT (email) DO NOTHING;

-- =============================================
-- DONE!
-- =============================================
-- Schema created successfully.
--
-- Tables created:
-- - organizations
-- - venues
-- - venue_database_configs
-- - venue_payment_configs
-- - platform_transactions
-- - platform_daily_revenue
-- - platform_monthly_revenue
-- - pull_staff
-- - pull_staff_sessions
-- - pull_staff_audit_log
-- - support_requests
-- - support_request_messages
-- - webhook_logs
-- =============================================
