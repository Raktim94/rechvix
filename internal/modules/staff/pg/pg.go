// Package pg is the staff module's PostgreSQL repository implementation.
// Same shape as internal/modules/pricing/pg.
package pg

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"rechvix/internal/modules/staff/domain"
	"rechvix/internal/platform/database"
)

// --- staff_members ---

type StaffMemberRepo struct{ pool *database.Pool }

func NewStaffMemberRepo(pool *database.Pool) *StaffMemberRepo { return &StaffMemberRepo{pool: pool} }

const staffMemberCols = `id, organisation_id, name, phone, role_title, is_active, created_at`

func scanStaffMember(row interface{ Scan(dest ...any) error }) (*domain.StaffMember, error) {
	var m domain.StaffMember
	err := row.Scan(&m.ID, &m.OrganisationID, &m.Name, &m.Phone, &m.RoleTitle, &m.IsActive, &m.CreatedAt)
	return &m, err
}

func (r *StaffMemberRepo) Create(ctx context.Context, m *domain.StaffMember) error {
	const q = `INSERT INTO staff_members (id, organisation_id, name, phone, role_title, is_active, created_at) VALUES ($1,$2,$3,$4,$5,$6,$7)`
	_, err := r.pool.Q(ctx).Exec(ctx, q, m.ID, m.OrganisationID, m.Name, m.Phone, m.RoleTitle, m.IsActive, m.CreatedAt)
	if err != nil {
		return fmt.Errorf("staff: inserting staff_member: %w", err)
	}
	return nil
}

func (r *StaffMemberRepo) GetByID(ctx context.Context, orgID, id uuid.UUID) (*domain.StaffMember, error) {
	q := fmt.Sprintf(`SELECT %s FROM staff_members WHERE organisation_id = $1 AND id = $2`, staffMemberCols)
	row := r.pool.Q(ctx).QueryRow(ctx, q, orgID, id)
	m, err := scanStaffMember(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("staff: getting staff_member: %w", err)
	}
	return m, nil
}

func (r *StaffMemberRepo) ListByOrganisation(ctx context.Context, orgID uuid.UUID) ([]*domain.StaffMember, error) {
	q := fmt.Sprintf(`SELECT %s FROM staff_members WHERE organisation_id = $1 ORDER BY name`, staffMemberCols)
	rows, err := r.pool.Q(ctx).Query(ctx, q, orgID)
	if err != nil {
		return nil, fmt.Errorf("staff: listing staff_members: %w", err)
	}
	defer rows.Close()
	var out []*domain.StaffMember
	for rows.Next() {
		m, err := scanStaffMember(rows)
		if err != nil {
			return nil, fmt.Errorf("staff: scanning staff_member row: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// --- staff_attendance ---

type AttendanceRepo struct{ pool *database.Pool }

func NewAttendanceRepo(pool *database.Pool) *AttendanceRepo { return &AttendanceRepo{pool: pool} }

// Upsert: marking the same (staff member, date) twice replaces the
// first mark — migrations/0037's UNIQUE(organisation_id,
// staff_member_id, attendance_date) is the actual enforcement, this is
// just the matching ON CONFLICT.
func (r *AttendanceRepo) Upsert(ctx context.Context, rec *domain.AttendanceRecord) error {
	const q = `
		INSERT INTO staff_attendance (id, organisation_id, staff_member_id, attendance_date, status, notes, marked_by, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$8)
		ON CONFLICT (organisation_id, staff_member_id, attendance_date)
		DO UPDATE SET status = EXCLUDED.status, notes = EXCLUDED.notes, marked_by = EXCLUDED.marked_by, updated_at = EXCLUDED.updated_at
		RETURNING id, created_at`
	row := r.pool.Q(ctx).QueryRow(ctx, q, rec.ID, rec.OrganisationID, rec.StaffMemberID, rec.AttendanceDate, string(rec.Status), rec.Notes, rec.MarkedBy, rec.CreatedAt)
	if err := row.Scan(&rec.ID, &rec.CreatedAt); err != nil {
		return fmt.Errorf("staff: upserting staff_attendance: %w", err)
	}
	return nil
}

func (r *AttendanceRepo) ListByDateRange(ctx context.Context, orgID uuid.UUID, from, to time.Time) ([]*domain.AttendanceRecord, error) {
	const q = `
		SELECT id, organisation_id, staff_member_id, attendance_date, status, notes, marked_by, created_at, updated_at
		FROM staff_attendance
		WHERE organisation_id = $1 AND attendance_date >= $2 AND attendance_date <= $3
		ORDER BY attendance_date`
	rows, err := r.pool.Q(ctx).Query(ctx, q, orgID, from, to)
	if err != nil {
		return nil, fmt.Errorf("staff: listing staff_attendance: %w", err)
	}
	defer rows.Close()
	var out []*domain.AttendanceRecord
	for rows.Next() {
		var rec domain.AttendanceRecord
		var status string
		if err := rows.Scan(&rec.ID, &rec.OrganisationID, &rec.StaffMemberID, &rec.AttendanceDate, &status, &rec.Notes, &rec.MarkedBy, &rec.CreatedAt, &rec.UpdatedAt); err != nil {
			return nil, fmt.Errorf("staff: scanning staff_attendance row: %w", err)
		}
		rec.Status = domain.AttendanceStatus(status)
		out = append(out, &rec)
	}
	return out, rows.Err()
}

// --- tasks ---

type TaskRepo struct{ pool *database.Pool }

func NewTaskRepo(pool *database.Pool) *TaskRepo { return &TaskRepo{pool: pool} }

const taskCols = `id, organisation_id, title, COALESCE(description, ''), due_date, assigned_to, status, created_by, created_at, completed_at`

func scanTask(row interface{ Scan(dest ...any) error }) (*domain.Task, error) {
	var t domain.Task
	var status string
	err := row.Scan(&t.ID, &t.OrganisationID, &t.Title, &t.Description, &t.DueDate, &t.AssignedTo, &status, &t.CreatedBy, &t.CreatedAt, &t.CompletedAt)
	t.Status = domain.TaskStatus(status)
	return &t, err
}

func (r *TaskRepo) Create(ctx context.Context, t *domain.Task) error {
	const q = `INSERT INTO tasks (id, organisation_id, title, description, due_date, assigned_to, status, created_by, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`
	_, err := r.pool.Q(ctx).Exec(ctx, q, t.ID, t.OrganisationID, t.Title, t.Description, t.DueDate, t.AssignedTo, string(t.Status), t.CreatedBy, t.CreatedAt)
	if err != nil {
		return fmt.Errorf("staff: inserting task: %w", err)
	}
	return nil
}

func (r *TaskRepo) GetByID(ctx context.Context, orgID, id uuid.UUID) (*domain.Task, error) {
	q := fmt.Sprintf(`SELECT %s FROM tasks WHERE organisation_id = $1 AND id = $2`, taskCols)
	row := r.pool.Q(ctx).QueryRow(ctx, q, orgID, id)
	t, err := scanTask(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("staff: getting task: %w", err)
	}
	return t, nil
}

func (r *TaskRepo) Update(ctx context.Context, t *domain.Task) error {
	const q = `UPDATE tasks SET title = $1, description = $2, due_date = $3, assigned_to = $4, status = $5, completed_at = $6
		WHERE organisation_id = $7 AND id = $8`
	rowsAffected, err := r.pool.Q(ctx).Exec(ctx, q, t.Title, t.Description, t.DueDate, t.AssignedTo, string(t.Status), t.CompletedAt, t.OrganisationID, t.ID)
	if err != nil {
		return fmt.Errorf("staff: updating task: %w", err)
	}
	if rowsAffected == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *TaskRepo) ListByDateRange(ctx context.Context, orgID uuid.UUID, from, to time.Time) ([]*domain.Task, error) {
	q := fmt.Sprintf(`SELECT %s FROM tasks WHERE organisation_id = $1 AND due_date >= $2 AND due_date <= $3 ORDER BY due_date`, taskCols)
	rows, err := r.pool.Q(ctx).Query(ctx, q, orgID, from, to)
	if err != nil {
		return nil, fmt.Errorf("staff: listing tasks: %w", err)
	}
	defer rows.Close()
	var out []*domain.Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, fmt.Errorf("staff: scanning task row: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
