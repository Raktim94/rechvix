# Zulivio workflow integration acceptance

Source: owned AGPL-3.0 reference at `Raktim94/zulivio`, backend employee,
attendance, assignment and assignment-rules services. This is an implementation
checklist, not a claim that the remaining workflows are available.

## Existing foundation verified 22 October 2026

Rechvix has organisation-scoped CRM opportunities, activities, customer conversion,
quotation handoff, staff records, daily attendance and assigned tasks. Login users
and staff records are distinct identities; do not silently equate their UUIDs.
CRM ownership uses active login users. Staff task assignment uses staff members.

Adapted Zulivio's organisation-reference validation into CRM ownership/customer
links and staff attendance/task creation. Foreign IDs must return not found and
leave no records. Tests: `TestStaff_RejectsForeignStaffReferences` and CRM foreign
reference integration regressions.

## Automatic lead ownership — outstanding

Port the behavior in `assignment-rules.service.ts`, not its Prisma schema:

- Rules have explicit active state, eligible members and positive SLA minutes.
- Round robin advances a persisted cursor atomically with lead creation; concurrent
  requests cannot all read the same old cursor.
- Territory matching trims/lowercases keys, validates mapped members and falls
  back to round robin when no territory matches.
- Capacity chooses the eligible member with fewest open leads, honours the
  configured cap (zero means no capacity), and leaves work unassigned when all
  members are full. A successful lead creation must not fail solely for capacity.
- Only users with CRM management permission edit rules. Validate unique members,
  same organisation, active membership and every territory target on create and
  update, including when the member list shrinks.
- Assignment records preserve who/what assigned the owner and an SLA deadline.
- Database acceptance must include tenant boundaries, disabled/deleted members,
  no active rule, all-at-capacity, concurrent cursor advancement, territory
  fallback and audited manual override. UI must expose rule configuration and the
  unassigned/overdue queue before marketing this as automatic routing.

## Timed attendance and breaks — outstanding

Reference `attendance.service.ts` supports session start, break start/end, session
end, current status and employee/team reports. Rechvix daily PRESENT/ABSENT/LEAVE
marks do not implement these behaviors.

- Introduce explicit staff-to-login linkage before employee self-service; preserve
  staff without accounts and existing daily records.
- Enforce at most one open session per staff member and one open break per session
  using database constraints/transaction locks, not read-then-insert checks alone.
- Ending a session closes its open break in the same transaction. Reject ending
  another member's break or an already-ended session.
- Store UTC instants; reports use the organisation timezone and clip sessions and
  breaks to the requested interval. Overnight and daylight-saving transitions must
  not create negative or double-counted net time.
- Self-service is restricted to the linked member. Managers require explicit staff
  permission; team reports remain organisation-scoped and audited as appropriate.
- Tests must cover parallel starts, double break starts, end-mid-break, tenant and
  identity boundaries, inactive members, interval clipping and overnight work.
- Add usable working/on-break/clocked-out UI and report/export flows; do not replace
  the existing daily attendance view or claim payroll/legal working-time compliance.

## Reporting hierarchy — outstanding

Inspect employees service scope and reporting lines before implementation. Prevent
cycles, cross-organisation supervisors and privilege escalation through reporting
relationships. A hierarchy must narrow an existing permission grant, never create
one. Test descendant visibility, reassignment and inactive supervisors.

Any new schema requires forward/backward migration checks on a disposable database
and a separately reviewed production rollout. No production migration is included
in the staff-reference correction.
