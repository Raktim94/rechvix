import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import ui from "../../components/ui.module.css";
import { api, ApiError } from "../../lib/api-client";
import { toLocalIsoDate } from "../../lib/calendarGrid";
import layout from "../DashboardPage.module.css";

interface StaffMember {
  ID: string;
  Name: string;
  Phone: string;
  RoleTitle: string;
  IsActive: boolean;
}

interface AttendanceRecord {
  StaffMemberID: string;
  AttendanceDate: string;
  Status: "PRESENT" | "ABSENT" | "LEAVE";
}

const SUMMARY_WINDOW_DAYS = 30;

export function StaffPage() {
  const queryClient = useQueryClient();
  const [name, setName] = useState("");
  const [phone, setPhone] = useState("");
  const [roleTitle, setRoleTitle] = useState("");

  const staff = useQuery({
    queryKey: ["staff-members"],
    queryFn: () => api.getListField<StaffMember>("/staff/members", "staff_members"),
  });

  const today = new Date();
  const from = toLocalIsoDate(new Date(today.getFullYear(), today.getMonth(), today.getDate() - SUMMARY_WINDOW_DAYS));
  const to = toLocalIsoDate(today);
  const attendance = useQuery({
    queryKey: ["staff-attendance", from, to],
    queryFn: () => api.getListField<AttendanceRecord>(`/staff/attendance?from=${from}&to=${to}`, "attendance"),
  });

  const create = useMutation({
    mutationFn: () => api.post("/staff/members", { name, phone, role_title: roleTitle }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["staff-members"] });
      setName("");
      setPhone("");
      setRoleTitle("");
    },
  });

  const attendanceCountsByStaff = new Map<string, { present: number; absent: number; leave: number }>();
  for (const a of attendance.data ?? []) {
    const counts = attendanceCountsByStaff.get(a.StaffMemberID) ?? { present: 0, absent: 0, leave: 0 };
    if (a.Status === "PRESENT") counts.present++;
    else if (a.Status === "ABSENT") counts.absent++;
    else counts.leave++;
    attendanceCountsByStaff.set(a.StaffMemberID, counts);
  }

  return (
    <div className={layout.page}>
      <div className={layout.heading}>
        <div>
          <h1>Staff</h1>
          <p className={layout.subtitle}>Who works here, and their attendance over the last {SUMMARY_WINDOW_DAYS} days.</p>
        </div>
      </div>

      <div className={layout.panel}>
        <h2>Add a staff member</h2>
        <form
          onSubmit={(e) => {
            e.preventDefault();
            if (name.trim()) create.mutate();
          }}
        >
          <div className={ui.formGrid}>
            <div className={ui.field}>
              <label htmlFor="staff-name">Name</label>
              <input id="staff-name" className={ui.input} value={name} onChange={(e) => setName(e.target.value)} required />
            </div>
            <div className={ui.field}>
              <label htmlFor="staff-phone">Phone</label>
              <input id="staff-phone" className={ui.input} value={phone} onChange={(e) => setPhone(e.target.value)} />
            </div>
            <div className={ui.field}>
              <label htmlFor="staff-role">Role</label>
              <input id="staff-role" className={ui.input} value={roleTitle} onChange={(e) => setRoleTitle(e.target.value)} placeholder="e.g. Helper, Delivery" />
            </div>
          </div>
          <div className={ui.formActions} style={{ marginTop: 16 }}>
            <button type="submit" className={ui.btnPrimary} disabled={!name.trim() || create.isPending}>
              {create.isPending ? "Adding…" : "Add staff member"}
            </button>
          </div>
          {create.isError ? (
            <p role="alert" style={{ color: "var(--color-negative)", marginTop: 8 }}>
              {create.error instanceof ApiError ? create.error.message : "Could not add this staff member."}
            </p>
          ) : null}
        </form>
      </div>

      <div className={layout.panel}>
        <h2>Staff summary</h2>
        {staff.isPending ? (
          <div className={layout.skeleton} style={{ height: 160 }} aria-hidden="true" />
        ) : (staff.data ?? []).length === 0 ? (
          <p className={layout.emptyState}>No staff members yet.</p>
        ) : (
          <div className={ui.tableScroll}>
            <table className={ui.table}>
              <thead>
                <tr>
                  <th scope="col">Name</th>
                  <th scope="col">Role</th>
                  <th scope="col">Phone</th>
                  <th scope="col">Present</th>
                  <th scope="col">Absent</th>
                  <th scope="col">Leave</th>
                </tr>
              </thead>
              <tbody>
                {(staff.data ?? []).map((m) => {
                  const counts = attendanceCountsByStaff.get(m.ID) ?? { present: 0, absent: 0, leave: 0 };
                  return (
                    <tr key={m.ID}>
                      <td>{m.Name}</td>
                      <td>{m.RoleTitle || "—"}</td>
                      <td>{m.Phone || "—"}</td>
                      <td className="num">{counts.present}</td>
                      <td className="num">{counts.absent}</td>
                      <td className="num">{counts.leave}</td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </div>
  );
}
