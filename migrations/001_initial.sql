CREATE TABLE organizations (
    id text PRIMARY KEY,
    name text NOT NULL,
    country_code text NOT NULL,
    kind text NOT NULL CHECK (kind IN ('government','research','enterprise','ngo','community','international')),
    active boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    UNIQUE (country_code, name)
);

CREATE TABLE users (
    id text PRIMARY KEY,
    organization_id text NOT NULL REFERENCES organizations(id),
    email text NOT NULL UNIQUE,
    password_hash text NOT NULL,
    role text NOT NULL CHECK (role IN ('platform_admin','program_manager','field_officer','technical_reviewer','finance_reviewer')),
    active boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL
);

CREATE TABLE sessions (
    id text PRIMARY KEY,
    user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash text NOT NULL UNIQUE,
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    created_at timestamptz NOT NULL
);
CREATE INDEX sessions_user_active_idx ON sessions(user_id, expires_at) WHERE revoked_at IS NULL;

CREATE TABLE programs (
    id text PRIMARY KEY,
    owner_organization_id text NOT NULL REFERENCES organizations(id),
    name text NOT NULL,
    starts_on date NOT NULL,
    ends_on date NOT NULL,
    status text NOT NULL CHECK (status IN ('draft','active','suspended','closed')),
    budget_cents bigint NOT NULL CHECK (budget_cents >= 0),
    version bigint NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    CHECK (ends_on > starts_on),
    UNIQUE(owner_organization_id, name)
);

CREATE TABLE partnerships (
    id text PRIMARY KEY,
    program_id text NOT NULL REFERENCES programs(id) ON DELETE CASCADE,
    organization_id text NOT NULL REFERENCES organizations(id),
    role text NOT NULL CHECK (role IN ('coordinator','research','implementation','funding','observer')),
    status text NOT NULL CHECK (status IN ('invited','active','suspended','ended')),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    UNIQUE(program_id, organization_id)
);

CREATE TABLE sites (
    id text PRIMARY KEY,
    program_id text NOT NULL REFERENCES programs(id),
    organization_id text NOT NULL REFERENCES organizations(id),
    name text NOT NULL,
    country_code text NOT NULL,
    area_hectares numeric(14,2) NOT NULL CHECK (area_hectares > 0),
    ecosystem text NOT NULL CHECK (ecosystem IN ('dryland','grassland','wetland','forest_edge','oasis')),
    status text NOT NULL CHECK (status IN ('proposed','approved','active','under_review','restored','archived')),
    version bigint NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    UNIQUE(program_id, name)
);
CREATE INDEX sites_program_status_idx ON sites(program_id, status);

CREATE TABLE monitoring_campaigns (
    id text PRIMARY KEY,
    site_id text NOT NULL REFERENCES sites(id),
    cycle_key text NOT NULL,
    status text NOT NULL CHECK (status IN ('planned','collecting','submitted','accepted','rejected','cancelled')),
    baseline_version bigint NOT NULL,
    started_at timestamptz,
    submitted_at timestamptz,
    version bigint NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    UNIQUE(site_id, cycle_key)
);

CREATE TABLE observations (
    id text PRIMARY KEY,
    campaign_id text NOT NULL REFERENCES monitoring_campaigns(id) ON DELETE CASCADE,
    kind text NOT NULL CHECK (kind IN ('vegetation','soil','water','biodiversity','dust')),
    measured_at timestamptz NOT NULL,
    value numeric(18,6) NOT NULL,
    unit text NOT NULL,
    quality text NOT NULL CHECK (quality IN ('raw','validated','rejected')),
    created_at timestamptz NOT NULL
);
CREATE INDEX observations_campaign_idx ON observations(campaign_id, measured_at);

CREATE TABLE intervention_plans (
    id text PRIMARY KEY,
    site_id text NOT NULL REFERENCES sites(id),
    title text NOT NULL,
    status text NOT NULL CHECK (status IN ('draft','approved','running','completed','cancelled')),
    estimated_cost_cents bigint NOT NULL CHECK (estimated_cost_cents >= 0),
    version bigint NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL
);

CREATE TABLE work_orders (
    id text PRIMARY KEY,
    plan_id text NOT NULL REFERENCES intervention_plans(id),
    site_id text NOT NULL REFERENCES sites(id),
    sequence_no integer NOT NULL CHECK (sequence_no > 0),
    title text NOT NULL,
    status text NOT NULL CHECK (status IN ('scheduled','claimed','running','completed','failed','cancelled')),
    owner_token text,
    lease_expires_at timestamptz,
    result_summary text,
    version bigint NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    UNIQUE(plan_id, sequence_no)
);
CREATE INDEX work_orders_claim_idx ON work_orders(status, lease_expires_at);

CREATE TABLE evidence_bundles (
    id text PRIMARY KEY,
    site_id text NOT NULL REFERENCES sites(id),
    campaign_id text REFERENCES monitoring_campaigns(id),
    work_order_id text REFERENCES work_orders(id),
    revision integer NOT NULL CHECK (revision > 0),
    status text NOT NULL CHECK (status IN ('draft','sealed','in_review','accepted','rejected','superseded')),
    digest text,
    sealed_at timestamptz,
    version bigint NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    UNIQUE(site_id, revision)
);

CREATE TABLE evidence_items (
    id text PRIMARY KEY,
    bundle_id text NOT NULL REFERENCES evidence_bundles(id) ON DELETE CASCADE,
    kind text NOT NULL CHECK (kind IN ('observation','remote_asset','field_note','completion_record')),
    object_key text NOT NULL,
    checksum text NOT NULL,
    created_at timestamptz NOT NULL,
    UNIQUE(bundle_id, object_key)
);

CREATE TABLE reviews (
    id text PRIMARY KEY,
    bundle_id text NOT NULL REFERENCES evidence_bundles(id),
    reviewer_id text NOT NULL REFERENCES users(id),
    round integer NOT NULL CHECK (round > 0),
    status text NOT NULL CHECK (status IN ('assigned','in_progress','accepted','rejected','withdrawn')),
    conclusion text,
    bundle_version bigint NOT NULL,
    version bigint NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    UNIQUE(bundle_id, reviewer_id, round)
);

CREATE TABLE grant_milestones (
    id text PRIMARY KEY,
    program_id text NOT NULL REFERENCES programs(id),
    site_id text NOT NULL REFERENCES sites(id),
    required_bundle_id text REFERENCES evidence_bundles(id),
    title text NOT NULL,
    amount_cents bigint NOT NULL CHECK (amount_cents > 0),
    status text NOT NULL CHECK (status IN ('planned','eligible','reserved','disbursed','cancelled')),
    version bigint NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL
);

CREATE TABLE budget_reservations (
    id text PRIMARY KEY,
    milestone_id text NOT NULL UNIQUE REFERENCES grant_milestones(id),
    program_id text NOT NULL REFERENCES programs(id),
    amount_cents bigint NOT NULL CHECK (amount_cents > 0),
    status text NOT NULL CHECK (status IN ('held','released','consumed')),
    expires_at timestamptz NOT NULL,
    version bigint NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL
);
CREATE INDEX reservations_program_status_idx ON budget_reservations(program_id, status);

CREATE TABLE alerts (
    id text PRIMARY KEY,
    program_id text NOT NULL REFERENCES programs(id),
    kind text NOT NULL CHECK (kind IN ('dust','drought','flood','wildfire')),
    severity text NOT NULL CHECK (severity IN ('advisory','watch','warning','emergency')),
    status text NOT NULL CHECK (status IN ('draft','published','resolved','expired')),
    starts_at timestamptz NOT NULL,
    ends_at timestamptz NOT NULL,
    version bigint NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    CHECK(ends_at > starts_at)
);

CREATE TABLE alert_sites (
    alert_id text NOT NULL REFERENCES alerts(id) ON DELETE CASCADE,
    site_id text NOT NULL REFERENCES sites(id),
    PRIMARY KEY(alert_id, site_id)
);

CREATE TABLE alert_acknowledgements (
    alert_id text NOT NULL REFERENCES alerts(id) ON DELETE CASCADE,
    organization_id text NOT NULL REFERENCES organizations(id),
    acknowledged_by text NOT NULL REFERENCES users(id),
    acknowledged_at timestamptz NOT NULL,
    PRIMARY KEY(alert_id, organization_id)
);

CREATE TABLE technology_transfers (
    id text PRIMARY KEY,
    program_id text NOT NULL REFERENCES programs(id),
    site_id text REFERENCES sites(id),
    title text NOT NULL,
    technology_version text NOT NULL,
    status text NOT NULL CHECK (status IN ('proposed','approved','deploying','deployed','retired')),
    version bigint NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    UNIQUE(program_id, title, technology_version)
);

CREATE TABLE training_cohorts (
    id text PRIMARY KEY,
    program_id text NOT NULL REFERENCES programs(id),
    title text NOT NULL,
    capacity integer NOT NULL CHECK (capacity > 0),
    status text NOT NULL CHECK (status IN ('scheduled','enrolling','running','completed','cancelled')),
    starts_at timestamptz NOT NULL,
    ends_at timestamptz NOT NULL,
    version bigint NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    CHECK(ends_at > starts_at)
);

CREATE TABLE training_attendance (
    cohort_id text NOT NULL REFERENCES training_cohorts(id) ON DELETE CASCADE,
    user_id text NOT NULL REFERENCES users(id),
    status text NOT NULL CHECK (status IN ('registered','attended','absent')),
    recorded_at timestamptz,
    PRIMARY KEY(cohort_id, user_id)
);

CREATE TABLE outbox_events (
    id text PRIMARY KEY,
    topic text NOT NULL,
    aggregate_id text NOT NULL,
    payload jsonb NOT NULL,
    status text NOT NULL CHECK (status IN ('pending','leased','delivered','retry','dead')),
    attempts integer NOT NULL DEFAULT 0,
    owner_token text,
    lease_expires_at timestamptz,
    available_at timestamptz NOT NULL,
    delivered_at timestamptz,
    last_error text,
    created_at timestamptz NOT NULL,
    UNIQUE(topic, aggregate_id, payload)
);
CREATE INDEX outbox_claim_idx ON outbox_events(status, available_at, lease_expires_at);

CREATE TABLE jobs (
    id text PRIMARY KEY,
    kind text NOT NULL,
    subject_id text NOT NULL,
    idempotency_key text NOT NULL UNIQUE,
    payload jsonb NOT NULL,
    status text NOT NULL CHECK (status IN ('pending','running','retry','succeeded','dead')),
    attempts integer NOT NULL DEFAULT 0,
    owner_token text,
    lease_expires_at timestamptz,
    available_at timestamptz NOT NULL,
    last_error text,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL
);
CREATE INDEX jobs_claim_idx ON jobs(status, available_at, lease_expires_at);

CREATE TABLE audit_entries (
    id text PRIMARY KEY,
    organization_id text REFERENCES organizations(id),
    actor_id text,
    action text NOT NULL,
    resource_type text NOT NULL,
    resource_id text NOT NULL,
    details jsonb NOT NULL,
    created_at timestamptz NOT NULL
);
CREATE INDEX audit_resource_idx ON audit_entries(resource_type, resource_id, created_at);

CREATE TABLE idempotency_records (
    id text PRIMARY KEY,
    organization_id text NOT NULL REFERENCES organizations(id),
    method text NOT NULL,
    path text NOT NULL,
    request_key text NOT NULL,
    request_hash text NOT NULL,
    response_status integer,
    response_body bytea,
    state text NOT NULL CHECK (state IN ('processing','completed','failed')),
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    UNIQUE(organization_id, method, path, request_key)
);
