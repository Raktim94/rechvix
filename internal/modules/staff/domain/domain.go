// Package domain is the staff module's domain layer — staff members,
// their daily attendance, and tasks (dated to-dos, optionally assigned
// to a staff member). Deliberately independent of
// internal/modules/identity's team_members (login accounts): see
// migrations/0037's own comment for why these are two different
// concepts, not two names for the same one.
package domain

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

var ErrNotFound = errors.New("staff: not found")

type StaffMember struct {
	ID             uuid.UUID
	OrganisationID uuid.UUID
	Name           string
	Phone          string
	RoleTitle      string
	IsActive       bool
	CreatedAt      time.Time
}

type StaffMemberRepository interface {
	Create(ctx context.Context, m *StaffMember) error
	GetByID(ctx context.Context, orgID, id uuid.UUID) (*StaffMember, error)
	ListByOrganisation(ctx context.Context, orgID uuid.UUID) ([]*StaffMember, error)
}

type AttendanceStatus string

const (
	AttendancePresent AttendanceStatus = "PRESENT"
	AttendanceAbsent  AttendanceStatus = "ABSENT"
	AttendanceLeave   AttendanceStatus = "LEAVE"
)

var ErrInvalidAttendanceStatus = errors.New("staff: status must be PRESENT, ABSENT, or LEAVE")

func (s AttendanceStatus) Valid() bool {
	return s == AttendancePresent || s == AttendanceAbsent || s == AttendanceLeave
}

type AttendanceRecord struct {
	ID             uuid.UUID
	OrganisationID uuid.UUID
	StaffMemberID  uuid.UUID
	AttendanceDate time.Time
	Status         AttendanceStatus
	Notes          string
	MarkedBy       uuid.UUID
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type AttendanceRepository interface {
	// Upsert is the owner-corrects-a-mistake path too — marking the same
	// (staffMemberID, date) a second time replaces the first mark rather
	// than growing a history, per migration 0037's UNIQUE constraint.
	Upsert(ctx context.Context, r *AttendanceRecord) error
	ListByDateRange(ctx context.Context, orgID uuid.UUID, from, to time.Time) ([]*AttendanceRecord, error)
}

type TaskStatus string

const (
	TaskPending TaskStatus = "PENDING"
	TaskDone    TaskStatus = "DONE"
)

type Task struct {
	ID             uuid.UUID
	OrganisationID uuid.UUID
	Title          string
	Description    string
	DueDate        time.Time
	AssignedTo     *uuid.UUID
	Status         TaskStatus
	CreatedBy      uuid.UUID
	CreatedAt      time.Time
	CompletedAt    *time.Time
}

type TaskRepository interface {
	Create(ctx context.Context, t *Task) error
	GetByID(ctx context.Context, orgID, id uuid.UUID) (*Task, error)
	Update(ctx context.Context, t *Task) error
	ListByDateRange(ctx context.Context, orgID uuid.UUID, from, to time.Time) ([]*Task, error)
}
