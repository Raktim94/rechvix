// Package app is the staff module's application/use-case layer. Same
// shape as internal/modules/pricing/app.
package app

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"rechvix/internal/modules/staff/domain"
	"rechvix/internal/platform/audit"
	"rechvix/internal/platform/database"
	"rechvix/internal/platform/permissions"
)

type Service struct {
	pool        database.Runner
	members     domain.StaffMemberRepository
	attendance  domain.AttendanceRepository
	tasks       domain.TaskRepository
	permissions *permissions.Checker
	audit       audit.Recorder
	now         func() time.Time
}

func NewService(
	pool database.Runner,
	members domain.StaffMemberRepository,
	attendance domain.AttendanceRepository,
	tasks domain.TaskRepository,
	checker *permissions.Checker,
	recorder audit.Recorder,
) *Service {
	return &Service{pool: pool, members: members, attendance: attendance, tasks: tasks, permissions: checker, audit: recorder, now: time.Now}
}

func (s *Service) view(ctx context.Context, principal permissions.Principal) error {
	return s.permissions.Require(ctx, principal, "staff.view", permissions.Scope{})
}

func (s *Service) manage(ctx context.Context, principal permissions.Principal) error {
	return s.permissions.Require(ctx, principal, "staff.manage", permissions.Scope{})
}

// --- Staff members ---

type CreateStaffMemberParams struct {
	Name      string
	Phone     string
	RoleTitle string
}

func (s *Service) CreateStaffMember(ctx context.Context, principal permissions.Principal, p CreateStaffMemberParams) (*domain.StaffMember, error) {
	if err := s.manage(ctx, principal); err != nil {
		return nil, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("staff: generating staff_member id: %w", err)
	}
	m := &domain.StaffMember{
		ID: id, OrganisationID: principal.OrganisationID, Name: p.Name, Phone: p.Phone, RoleTitle: p.RoleTitle,
		IsActive: true, CreatedAt: s.now(),
	}
	err = s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		if err := s.members.Create(ctx, m); err != nil {
			return err
		}
		return s.audit.Record(ctx, audit.Entry{
			OrganisationID: principal.OrganisationID, ActorUserID: &principal.UserID, ActorType: audit.ActorUser,
			Action: "staff.member_created", EntityType: "staff_member", EntityID: &id,
			AfterState: map[string]any{"name": p.Name}, At: m.CreatedAt,
		})
	})
	if err != nil {
		return nil, err
	}
	return m, nil
}

func (s *Service) ListStaffMembers(ctx context.Context, principal permissions.Principal) ([]*domain.StaffMember, error) {
	if err := s.view(ctx, principal); err != nil {
		return nil, err
	}
	var out []*domain.StaffMember
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		var err error
		out, err = s.members.ListByOrganisation(ctx, principal.OrganisationID)
		return err
	})
	return out, err
}

// --- Attendance ---

type MarkAttendanceParams struct {
	StaffMemberID  uuid.UUID
	AttendanceDate time.Time
	Status         domain.AttendanceStatus
	Notes          string
}

func (s *Service) MarkAttendance(ctx context.Context, principal permissions.Principal, p MarkAttendanceParams) (*domain.AttendanceRecord, error) {
	if err := s.manage(ctx, principal); err != nil {
		return nil, err
	}
	if !p.Status.Valid() {
		return nil, domain.ErrInvalidAttendanceStatus
	}
	id, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("staff: generating staff_attendance id: %w", err)
	}
	now := s.now()
	r := &domain.AttendanceRecord{
		ID: id, OrganisationID: principal.OrganisationID, StaffMemberID: p.StaffMemberID, AttendanceDate: p.AttendanceDate,
		Status: p.Status, Notes: p.Notes, MarkedBy: principal.UserID, CreatedAt: now, UpdatedAt: now,
	}
	err = s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		if err := s.attendance.Upsert(ctx, r); err != nil {
			return err
		}
		return s.audit.Record(ctx, audit.Entry{
			OrganisationID: principal.OrganisationID, ActorUserID: &principal.UserID, ActorType: audit.ActorUser,
			Action: "staff.attendance_marked", EntityType: "staff_attendance", EntityID: &r.ID,
			AfterState: map[string]any{"staff_member_id": p.StaffMemberID, "date": p.AttendanceDate, "status": string(p.Status)}, At: now,
		})
	})
	if err != nil {
		return nil, err
	}
	return r, nil
}

func (s *Service) ListAttendance(ctx context.Context, principal permissions.Principal, from, to time.Time) ([]*domain.AttendanceRecord, error) {
	if err := s.view(ctx, principal); err != nil {
		return nil, err
	}
	var out []*domain.AttendanceRecord
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		var err error
		out, err = s.attendance.ListByDateRange(ctx, principal.OrganisationID, from, to)
		return err
	})
	return out, err
}

// --- Tasks ---

type CreateTaskParams struct {
	Title       string
	Description string
	DueDate     time.Time
	AssignedTo  *uuid.UUID
}

func (s *Service) CreateTask(ctx context.Context, principal permissions.Principal, p CreateTaskParams) (*domain.Task, error) {
	if err := s.manage(ctx, principal); err != nil {
		return nil, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("staff: generating task id: %w", err)
	}
	now := s.now()
	t := &domain.Task{
		ID: id, OrganisationID: principal.OrganisationID, Title: p.Title, Description: p.Description, DueDate: p.DueDate,
		AssignedTo: p.AssignedTo, Status: domain.TaskPending, CreatedBy: principal.UserID, CreatedAt: now,
	}
	err = s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		return s.tasks.Create(ctx, t)
	})
	if err != nil {
		return nil, err
	}
	return t, nil
}

func (s *Service) ListTasks(ctx context.Context, principal permissions.Principal, from, to time.Time) ([]*domain.Task, error) {
	if err := s.view(ctx, principal); err != nil {
		return nil, err
	}
	var out []*domain.Task
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		var err error
		out, err = s.tasks.ListByDateRange(ctx, principal.OrganisationID, from, to)
		return err
	})
	return out, err
}

func (s *Service) SetTaskStatus(ctx context.Context, principal permissions.Principal, taskID uuid.UUID, status domain.TaskStatus) (*domain.Task, error) {
	if err := s.manage(ctx, principal); err != nil {
		return nil, err
	}
	var t *domain.Task
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		var err error
		t, err = s.tasks.GetByID(ctx, principal.OrganisationID, taskID)
		if err != nil {
			return err
		}
		t.Status = status
		if status == domain.TaskDone {
			now := s.now()
			t.CompletedAt = &now
		} else {
			t.CompletedAt = nil
		}
		return s.tasks.Update(ctx, t)
	})
	if err != nil {
		return nil, err
	}
	return t, nil
}
