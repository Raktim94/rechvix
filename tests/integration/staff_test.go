//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	staffapp "rechvix/internal/modules/staff/app"
	staffdomain "rechvix/internal/modules/staff/domain"
	staffpg "rechvix/internal/modules/staff/pg"
	"rechvix/internal/platform/audit"
	"rechvix/internal/platform/permissions"
)

func newTestStaffService(t *testing.T) *staffapp.Service {
	t.Helper()
	return staffapp.NewService(
		sharedPool,
		staffpg.NewStaffMemberRepo(sharedPool),
		staffpg.NewAttendanceRepo(sharedPool),
		staffpg.NewTaskRepo(sharedPool),
		permissions.NewChecker(permissions.NewPGStore(sharedPool), sharedPool),
		audit.NewPGRecorder(sharedPool),
	)
}

func setupStaffPrincipal(t *testing.T, ctx context.Context) permissions.Principal {
	t.Helper()
	identitySvc, _ := newTestIdentityService(t)
	email := "staff-" + uuid.NewString()[:8] + "@example.com"
	boot := bootstrapTestTenant(t, ctx, identitySvc, email, "correct horse battery staple 42")
	return permissions.Principal{UserID: boot.OwnerUserID, OrganisationID: boot.OrganisationID}
}

func mustDate(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		t.Fatalf("parsing date %q: %v", s, err)
	}
	return d
}

// TestStaff_MarkAttendance_UpsertReplacesNotGrows proves the owner's
// "correct a mistake by marking the same day again" path: marking the
// same staff member on the same date twice, with different statuses,
// results in exactly one attendance row for that day, holding the
// latest status.
func TestStaff_MarkAttendance_UpsertReplacesNotGrows(t *testing.T) {
	ctx := context.Background()
	svc := newTestStaffService(t)
	principal := setupStaffPrincipal(t, ctx)

	member, err := svc.CreateStaffMember(ctx, principal, staffapp.CreateStaffMemberParams{Name: "Ramesh", Phone: "9876543210", RoleTitle: "Helper"})
	if err != nil {
		t.Fatalf("CreateStaffMember: %v", err)
	}

	date := mustDate(t, "2026-09-01")
	if _, err := svc.MarkAttendance(ctx, principal, staffapp.MarkAttendanceParams{StaffMemberID: member.ID, AttendanceDate: date, Status: staffdomain.AttendanceAbsent}); err != nil {
		t.Fatalf("MarkAttendance(ABSENT): %v", err)
	}
	if _, err := svc.MarkAttendance(ctx, principal, staffapp.MarkAttendanceParams{StaffMemberID: member.ID, AttendanceDate: date, Status: staffdomain.AttendancePresent, Notes: "came in late, correcting"}); err != nil {
		t.Fatalf("MarkAttendance(PRESENT, correction): %v", err)
	}

	records, err := svc.ListAttendance(ctx, principal, date, date)
	if err != nil {
		t.Fatalf("ListAttendance: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("len(records) = %d, want exactly 1 (upsert, not a growing history)", len(records))
	}
	if records[0].Status != staffdomain.AttendancePresent {
		t.Fatalf("records[0].Status = %s, want PRESENT (the correction)", records[0].Status)
	}
}

// TestStaff_MarkAttendance_RejectsInvalidStatus proves the service-layer
// validation (not just the DB CHECK constraint) rejects a bad status
// before ever reaching the database.
func TestStaff_MarkAttendance_RejectsInvalidStatus(t *testing.T) {
	ctx := context.Background()
	svc := newTestStaffService(t)
	principal := setupStaffPrincipal(t, ctx)

	member, err := svc.CreateStaffMember(ctx, principal, staffapp.CreateStaffMemberParams{Name: "Suresh"})
	if err != nil {
		t.Fatalf("CreateStaffMember: %v", err)
	}
	_, err = svc.MarkAttendance(ctx, principal, staffapp.MarkAttendanceParams{
		StaffMemberID: member.ID, AttendanceDate: mustDate(t, "2026-09-01"), Status: staffdomain.AttendanceStatus("ON_VACATION"),
	})
	if err != staffdomain.ErrInvalidAttendanceStatus {
		t.Fatalf("MarkAttendance(bad status) error = %v, want ErrInvalidAttendanceStatus", err)
	}
}

// TestStaff_Tasks_CreateListAndComplete covers both task shapes (a
// plain dated to-do and one assigned to a staff member), that both show
// up in the same date-ranged calendar list, and that completing one
// sets CompletedAt without disturbing the other.
func TestStaff_Tasks_CreateListAndComplete(t *testing.T) {
	ctx := context.Background()
	svc := newTestStaffService(t)
	principal := setupStaffPrincipal(t, ctx)

	member, err := svc.CreateStaffMember(ctx, principal, staffapp.CreateStaffMemberParams{Name: "Priya"})
	if err != nil {
		t.Fatalf("CreateStaffMember: %v", err)
	}

	due := mustDate(t, "2026-09-10")
	plainTask, err := svc.CreateTask(ctx, principal, staffapp.CreateTaskParams{Title: "Pay electricity bill", DueDate: due})
	if err != nil {
		t.Fatalf("CreateTask(plain): %v", err)
	}
	assignedTask, err := svc.CreateTask(ctx, principal, staffapp.CreateTaskParams{Title: "Restock cement", DueDate: due, AssignedTo: &member.ID})
	if err != nil {
		t.Fatalf("CreateTask(assigned): %v", err)
	}

	tasks, err := svc.ListTasks(ctx, principal, due, due)
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("len(tasks) = %d, want 2", len(tasks))
	}
	for _, task := range tasks {
		if task.Status != staffdomain.TaskPending {
			t.Fatalf("task %q status = %s, want PENDING before completion", task.Title, task.Status)
		}
	}

	completed, err := svc.SetTaskStatus(ctx, principal, assignedTask.ID, staffdomain.TaskDone)
	if err != nil {
		t.Fatalf("SetTaskStatus(DONE): %v", err)
	}
	if completed.Status != staffdomain.TaskDone || completed.CompletedAt == nil {
		t.Fatalf("completed task = %+v, want status DONE with CompletedAt set", completed)
	}

	tasks, err = svc.ListTasks(ctx, principal, due, due)
	if err != nil {
		t.Fatalf("ListTasks after completion: %v", err)
	}
	var stillPending, done int
	for _, task := range tasks {
		if task.ID == plainTask.ID && task.Status != staffdomain.TaskPending {
			t.Fatalf("completing the assigned task should not have affected the unrelated plain task")
		}
		if task.Status == staffdomain.TaskPending {
			stillPending++
		} else {
			done++
		}
	}
	if stillPending != 1 || done != 1 {
		t.Fatalf("pending=%d done=%d, want 1 and 1", stillPending, done)
	}
}

// TestStaff_RLS_BlocksCrossOrganisationReads follows the same
// direct-RLS-proof shape as TestRLS_Sweep_Stage4Tables: a row created
// under org A must be invisible to a raw, org-B-scoped query that names
// its exact id, for each of the three new tables.
func TestStaff_RLS_BlocksCrossOrganisationReads(t *testing.T) {
	ctx := context.Background()
	svc := newTestStaffService(t)
	principalA := setupStaffPrincipal(t, ctx)
	principalB := setupStaffPrincipal(t, ctx)

	member, err := svc.CreateStaffMember(ctx, principalA, staffapp.CreateStaffMemberParams{Name: "Org A's Staffer"})
	if err != nil {
		t.Fatalf("CreateStaffMember: %v", err)
	}
	assertInvisibleToOtherOrg(t, ctx, "staff_members", member.ID, principalB.OrganisationID)

	rec, err := svc.MarkAttendance(ctx, principalA, staffapp.MarkAttendanceParams{StaffMemberID: member.ID, AttendanceDate: mustDate(t, "2026-09-01"), Status: staffdomain.AttendancePresent})
	if err != nil {
		t.Fatalf("MarkAttendance: %v", err)
	}
	assertInvisibleToOtherOrg(t, ctx, "staff_attendance", rec.ID, principalB.OrganisationID)

	task, err := svc.CreateTask(ctx, principalA, staffapp.CreateTaskParams{Title: "Org A's task", DueDate: mustDate(t, "2026-09-01")})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	assertInvisibleToOtherOrg(t, ctx, "tasks", task.ID, principalB.OrganisationID)
}
