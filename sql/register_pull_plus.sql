-- =============================================
-- REGISTER PULL-PLUS VENUE IN PULL-CENTRAL
-- =============================================
-- Execute this in: https://supabase.com/dashboard/project/dqqvtehpidihahzabcxg/sql
-- =============================================

-- 1. First create an owner user for the organization
INSERT INTO public_users (
    id,
    email,
    name,
    surname
) VALUES (
    'e0000000-0000-0000-0000-000000000001',
    'owner@pullplus.com',
    'Pull Plus',
    'Owner'
) ON CONFLICT (id) DO NOTHING;

-- 2. Create the organization
INSERT INTO organizations (
    id,
    owner_id,
    name,
    legal_name,
    email,
    phone,
    country
) VALUES (
    'e0000000-0000-0000-0000-000000000002',
    'e0000000-0000-0000-0000-000000000001',
    'Pull Plus',
    'Pull Plus S.A.',
    'admin@pullplus.com',
    '+502 0000 0000',
    'Guatemala'
) ON CONFLICT (id) DO UPDATE SET
    name = EXCLUDED.name,
    updated_at = NOW();

-- 3. Create the venue (VIP Lists ONLY)
INSERT INTO venues (
    id,
    organization_id,
    name,
    slug,
    description,
    location,
    city,
    country,
    open_time,
    close_time,
    timezone,
    currency,
    platform_fee_percent,
    platform_fee_fixed,
    payment_gateway,
    use_vip_list_flow,
    use_guest_list_flow,
    use_individual_tickets,
    use_group_reservations,
    is_active
) VALUES (
    'f0000000-0000-0000-0000-000000000001',
    'e0000000-0000-0000-0000-000000000002',
    'Pull Plus',
    'pull-plus',
    'Venue exclusivo con sistema VIP List',
    'Guatemala City',
    'Guatemala City',
    'Guatemala',
    '20:00:00',
    '04:00:00',
    'America/Guatemala',
    'GTQ',
    5.00,
    0.00,
    'stripe',
    true,     -- SOLO VIP LISTS
    false,    -- Sin guest list flow
    false,    -- Sin tickets individuales
    false,    -- Sin group reservations
    true
) ON CONFLICT (slug) DO UPDATE SET
    name = EXCLUDED.name,
    use_vip_list_flow = true,
    use_guest_list_flow = false,
    use_individual_tickets = false,
    use_group_reservations = false,
    updated_at = NOW();

-- 4. Register the database connection (KEYS ENCRYPTED)
INSERT INTO venue_database_configs (
    id,
    venue_id,
    supabase_url,
    supabase_service_key_encrypted,
    supabase_anon_key,
    is_active,
    migration_status
) VALUES (
    gen_random_uuid(),
    'f0000000-0000-0000-0000-000000000001',
    'https://oqqhffxwiizukkevzkvz.supabase.co',
    'yexOj3zs9xqG2QCchpAroQBW9mx3d8E5Qvgtc575Z8NRw6R8bKBIp/qDo2aSfus5qR8/VmqK1x0yWiCwuVrWJQDHWHLz',
    'JLMZeAlJWiHbyh6Tr2HBgDyugeHx4hgI7M4zio8+5YsE0icGNorTQ4prwBfOzp3cXc58NIk6blNdjWOBM3CzvXCK82QdpixWf9s=',
    true,
    'pending'
) ON CONFLICT (venue_id) DO UPDATE SET
    supabase_url = EXCLUDED.supabase_url,
    supabase_service_key_encrypted = EXCLUDED.supabase_service_key_encrypted,
    supabase_anon_key = EXCLUDED.supabase_anon_key,
    is_active = true,
    updated_at = NOW();

-- 5. Verify
SELECT
    v.name as venue_name,
    v.slug,
    v.use_vip_list_flow,
    v.use_guest_list_flow,
    v.use_individual_tickets,
    vdc.supabase_url,
    vdc.is_active as db_active
FROM venues v
LEFT JOIN venue_database_configs vdc ON v.id = vdc.venue_id
WHERE v.slug = 'pull-plus';
