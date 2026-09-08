-- Staff module (new): lightweight person records for attendance/task
-- assignment that are DELIBERATELY separate from identity's team_members
-- (login accounts) — a shop's delivery boy or a warehouse helper who
-- never touches this software still needs to show up on an attendance
-- register and get handed a task, without needing an email/password and
-- a system role. If a staff member later also needs app access, that's
-- a completely independent team_members row; nothing here references
-- one.
INSERT INTO permissions (code, module, description) VALUES
    ('staff.view',   'staff', 'View staff members, attendance, and tasks'),
    ('staff.manage', 'staff', 'Add staff members, mark attendance, and manage tasks');

-- Same "system roles ship with a fixed starter permission set" backfill
-- migrations/0033 and 0035 already established.
INSERT INTO role_permissions (role_id, permission_code)
SELECT roles.id, p.code
FROM roles, (VALUES ('staff.view'), ('staff.manage')) AS p(code)
WHERE roles.is_system = true
ON CONFLICT DO NOTHING;

CREATE TABLE staff_members (
    id              uuid PRIMARY KEY,
    organisation_id uuid NOT NULL REFERENCES organisations(id),
    name            text NOT NULL,
    phone           text NOT NULL DEFAULT '',
    role_title      text NOT NULL DEFAULT '',
    is_active       boolean NOT NULL DEFAULT true,
    created_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_staff_members_organisation_id ON staff_members(organisation_id);

ALTER TABLE staff_members ENABLE ROW LEVEL SECURITY;
CREATE POLICY staff_members_tenant_isolation ON staff_members
    USING (organisation_id = NULLIF(current_setting('app.current_organisation_id', true), '')::uuid)
    WITH CHECK (organisation_id = NULLIF(current_setting('app.current_organisation_id', true), '')::uuid);

-- One row per (staff member, calendar day) — marking the same day twice
-- (the owner correcting a mistake) is an UPSERT via the unique
-- constraint below, not a growing history of corrections.
CREATE TABLE staff_attendance (
    id               uuid PRIMARY KEY,
    organisation_id  uuid NOT NULL REFERENCES organisations(id),
    staff_member_id  uuid NOT NULL REFERENCES staff_members(id),
    attendance_date  date NOT NULL,
    status           text NOT NULL CHECK (status IN ('PRESENT', 'ABSENT', 'LEAVE')),
    notes            text NOT NULL DEFAULT '',
    marked_by        uuid NOT NULL REFERENCES users(id),
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    UNIQUE (organisation_id, staff_member_id, attendance_date)
);

CREATE INDEX idx_staff_attendance_org_date ON staff_attendance(organisation_id, attendance_date);

ALTER TABLE staff_attendance ENABLE ROW LEVEL SECURITY;
CREATE POLICY staff_attendance_tenant_isolation ON staff_attendance
    USING (organisation_id = NULLIF(current_setting('app.current_organisation_id', true), '')::uuid)
    WITH CHECK (organisation_id = NULLIF(current_setting('app.current_organisation_id', true), '')::uuid);

-- Tasks show on the same calendar as attendance (brief: "a calendar for
-- important tasks... also staff attendance logged in calendar"),
-- support both a plain dated to-do (assigned_to NULL) and one handed to
-- a specific staff member — the same table either way, just an optional
-- assignee, rather than two parallel task concepts.
CREATE TABLE tasks (
    id              uuid PRIMARY KEY,
    organisation_id uuid NOT NULL REFERENCES organisations(id),
    title           text NOT NULL,
    description     text NOT NULL DEFAULT '',
    due_date        date NOT NULL,
    assigned_to     uuid REFERENCES staff_members(id),
    status          text NOT NULL DEFAULT 'PENDING' CHECK (status IN ('PENDING', 'DONE')),
    created_by      uuid NOT NULL REFERENCES users(id),
    created_at      timestamptz NOT NULL DEFAULT now(),
    completed_at    timestamptz
);

CREATE INDEX idx_tasks_org_due_date ON tasks(organisation_id, due_date);
CREATE INDEX idx_tasks_assigned_to ON tasks(assigned_to) WHERE assigned_to IS NOT NULL;

ALTER TABLE tasks ENABLE ROW LEVEL SECURITY;
CREATE POLICY tasks_tenant_isolation ON tasks
    USING (organisation_id = NULLIF(current_setting('app.current_organisation_id', true), '')::uuid)
    WITH CHECK (organisation_id = NULLIF(current_setting('app.current_organisation_id', true), '')::uuid);
