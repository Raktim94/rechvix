import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import ui from "../../components/ui.module.css";
import { api, ApiError } from "../../lib/api-client";
import { monthGrid, toLocalIsoDate } from "../../lib/calendarGrid";
import layout from "../DashboardPage.module.css";
import styles from "./Calendar.module.css";

interface StaffMember {
  ID: string;
  Name: string;
  Phone: string;
  RoleTitle: string;
  IsActive: boolean;
}

type AttendanceStatus = "PRESENT" | "ABSENT" | "LEAVE";

interface AttendanceRecord {
  ID: string;
  StaffMemberID: string;
  AttendanceDate: string;
  Status: AttendanceStatus;
  Notes: string;
}

interface Task {
  ID: string;
  Title: string;
  Description: string;
  DueDate: string;
  AssignedTo: string | null;
  Status: "PENDING" | "DONE";
}

const WEEKDAY_LABELS = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"];

const STATUS_OPTIONS: { status: AttendanceStatus; label: string; tone: "positive" | "negative" | "warning" }[] = [
  { status: "PRESENT", label: "Present", tone: "positive" },
  { status: "ABSENT", label: "Absent", tone: "negative" },
  { status: "LEAVE", label: "Leave", tone: "warning" },
];

/** A month calendar showing both staff attendance and tasks on the same
 * grid (per the brief: attendance should be "logged in calendar", not a
 * separate screen from tasks) — click a day to see/edit both. */
export function CalendarPage() {
  const queryClient = useQueryClient();
  const today = new Date();
  const [viewYear, setViewYear] = useState(today.getFullYear());
  const [viewMonth, setViewMonth] = useState(today.getMonth());
  const [selectedIso, setSelectedIso] = useState(toLocalIsoDate(today));
  const [newTaskTitle, setNewTaskTitle] = useState("");
  const [newTaskAssignee, setNewTaskAssignee] = useState("");

  const cells = monthGrid(viewYear, viewMonth);
  const rangeFrom = cells[0]?.iso ?? selectedIso;
  const rangeTo = cells[cells.length - 1]?.iso ?? selectedIso;

  const staff = useQuery({
    queryKey: ["staff-members"],
    queryFn: () => api.getListField<StaffMember>("/staff/members", "staff_members"),
  });
  const activeStaff = (staff.data ?? []).filter((m) => m.IsActive);

  const attendance = useQuery({
    queryKey: ["staff-attendance", rangeFrom, rangeTo],
    queryFn: () => api.getListField<AttendanceRecord>(`/staff/attendance?from=${rangeFrom}&to=${rangeTo}`, "attendance"),
  });

  const tasks = useQuery({
    queryKey: ["staff-tasks", rangeFrom, rangeTo],
    queryFn: () => api.getListField<Task>(`/staff/tasks?from=${rangeFrom}&to=${rangeTo}`, "tasks"),
  });

  const attendanceByDate = new Map<string, AttendanceRecord[]>();
  for (const a of attendance.data ?? []) {
    const iso = a.AttendanceDate.slice(0, 10);
    attendanceByDate.set(iso, [...(attendanceByDate.get(iso) ?? []), a]);
  }
  const tasksByDate = new Map<string, Task[]>();
  for (const t of tasks.data ?? []) {
    const iso = t.DueDate.slice(0, 10);
    tasksByDate.set(iso, [...(tasksByDate.get(iso) ?? []), t]);
  }
  const staffNameById = new Map(activeStaff.map((m) => [m.ID, m.Name]));

  const mark = useMutation({
    mutationFn: (vars: { staffMemberId: string; status: AttendanceStatus }) =>
      api.post("/staff/attendance", { staff_member_id: vars.staffMemberId, attendance_date: selectedIso, status: vars.status }),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["staff-attendance"] }),
  });

  const addTask = useMutation({
    mutationFn: () =>
      api.post("/staff/tasks", {
        title: newTaskTitle,
        due_date: selectedIso,
        assigned_to: newTaskAssignee || undefined,
      }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["staff-tasks"] });
      setNewTaskTitle("");
      setNewTaskAssignee("");
    },
  });

  const setTaskStatus = useMutation({
    mutationFn: (vars: { id: string; status: "PENDING" | "DONE" }) => api.post(`/staff/tasks/${vars.id}/status`, { status: vars.status }),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["staff-tasks"] }),
  });

  function changeMonth(delta: number) {
    const d = new Date(viewYear, viewMonth + delta, 1);
    setViewYear(d.getFullYear());
    setViewMonth(d.getMonth());
  }

  const selectedTasks = tasksByDate.get(selectedIso) ?? [];
  const selectedAttendance = attendanceByDate.get(selectedIso) ?? [];
  const statusByStaffId = new Map(selectedAttendance.map((a) => [a.StaffMemberID, a.Status]));

  return (
    <div className={layout.page}>
      <div className={layout.heading}>
        <div>
          <h1>Calendar</h1>
          <p className={layout.subtitle}>Staff attendance and tasks, day by day.</p>
        </div>
      </div>

      <div className={layout.panel}>
        <div className={styles.monthNav}>
          <button type="button" className={ui.btnSecondary} onClick={() => changeMonth(-1)} aria-label="Previous month">
            ‹
          </button>
          <span className={styles.monthLabel}>
            {new Date(viewYear, viewMonth, 1).toLocaleDateString(undefined, { month: "long", year: "numeric" })}
          </span>
          <button type="button" className={ui.btnSecondary} onClick={() => changeMonth(1)} aria-label="Next month">
            ›
          </button>
          <div className={ui.toolbarSpacer} />
          <button type="button" className={ui.btnSecondary} onClick={() => setSelectedIso(toLocalIsoDate(today))}>
            Today
          </button>
        </div>

        <div className={styles.grid}>
          {WEEKDAY_LABELS.map((w) => (
            <div key={w} className={styles.weekdayLabel}>
              {w}
            </div>
          ))}
          {cells.map((cell) => {
            const dayTasks = tasksByDate.get(cell.iso) ?? [];
            const dayAttendance = attendanceByDate.get(cell.iso) ?? [];
            const absentCount = dayAttendance.filter((a) => a.Status === "ABSENT").length;
            const pendingTaskCount = dayTasks.filter((t) => t.Status === "PENDING").length;
            return (
              <button
                key={cell.iso}
                type="button"
                className={styles.dayCell}
                data-outside={!cell.inMonth}
                data-today={cell.iso === toLocalIsoDate(today)}
                data-selected={cell.iso === selectedIso}
                onClick={() => setSelectedIso(cell.iso)}
              >
                <span className={styles.dayNumber}>{cell.date.getDate()}</span>
                <span className={styles.dayBadges}>
                  {pendingTaskCount > 0 ? (
                    <span className={styles.dayBadge} data-tone="neutral">
                      {pendingTaskCount} task{pendingTaskCount > 1 ? "s" : ""}
                    </span>
                  ) : null}
                  {absentCount > 0 ? (
                    <span className={styles.dayBadge} data-tone="negative">
                      {absentCount} absent
                    </span>
                  ) : null}
                </span>
              </button>
            );
          })}
        </div>
      </div>

      <div className={layout.panel}>
        <h2>{new Date(selectedIso).toLocaleDateString(undefined, { weekday: "long", day: "numeric", month: "long" })}</h2>
        <div className={styles.detailPanel}>
          <div>
            <p className={ui.muted} style={{ marginBottom: 8 }}>
              Attendance
            </p>
            {activeStaff.length === 0 ? (
              <p className={layout.emptyState}>No staff members yet — add one on the Staff page.</p>
            ) : (
              activeStaff.map((member) => {
                const current = statusByStaffId.get(member.ID);
                return (
                  <div key={member.ID} className={styles.attendanceRow}>
                    <span>{member.Name}</span>
                    <span className={styles.statusToggle}>
                      {STATUS_OPTIONS.map((opt) => (
                        <button
                          key={opt.status}
                          type="button"
                          className={styles.statusButton}
                          data-active={current === opt.status}
                          data-tone={opt.tone}
                          disabled={mark.isPending}
                          onClick={() => mark.mutate({ staffMemberId: member.ID, status: opt.status })}
                        >
                          {opt.label}
                        </button>
                      ))}
                    </span>
                  </div>
                );
              })
            )}
          </div>

          <div>
            <p className={ui.muted} style={{ marginBottom: 8 }}>
              Tasks
            </p>
            {selectedTasks.length === 0 ? <p className={layout.emptyState}>No tasks for this day yet.</p> : null}
            {selectedTasks.map((task) => (
              <div key={task.ID} className={styles.taskRow} data-done={task.Status === "DONE"}>
                <input
                  type="checkbox"
                  checked={task.Status === "DONE"}
                  onChange={(e) => setTaskStatus.mutate({ id: task.ID, status: e.target.checked ? "DONE" : "PENDING" })}
                  style={{ marginTop: 3 }}
                />
                <div>
                  <div className={styles.taskTitle}>{task.Title}</div>
                  {task.AssignedTo ? <div className={styles.taskMeta}>Assigned to {staffNameById.get(task.AssignedTo) ?? "—"}</div> : null}
                </div>
              </div>
            ))}
            <form
              style={{ display: "flex", gap: 8, marginTop: 12 }}
              onSubmit={(e) => {
                e.preventDefault();
                if (newTaskTitle.trim()) addTask.mutate();
              }}
            >
              <input
                className={ui.input}
                style={{ flex: 1 }}
                placeholder="New task for this day…"
                value={newTaskTitle}
                onChange={(e) => setNewTaskTitle(e.target.value)}
              />
              <select className={ui.select} value={newTaskAssignee} onChange={(e) => setNewTaskAssignee(e.target.value)} style={{ maxWidth: 160 }}>
                <option value="">Unassigned</option>
                {activeStaff.map((m) => (
                  <option key={m.ID} value={m.ID}>
                    {m.Name}
                  </option>
                ))}
              </select>
              <button type="submit" className={ui.btnPrimary} disabled={!newTaskTitle.trim() || addTask.isPending}>
                Add
              </button>
            </form>
            {addTask.isError ? (
              <p role="alert" style={{ color: "var(--color-negative)", marginTop: 8 }}>
                {addTask.error instanceof ApiError ? addTask.error.message : "Could not add this task."}
              </p>
            ) : null}
          </div>
        </div>
      </div>
    </div>
  );
}
