<!-- title: Capacity & Planning | order: 23 | category: Using gitstate | tier: Using gitstate | summary: Availability minus approved leave, plus git-aware time tracking. -->

# Capacity & Planning

Capacity in gitstate is computed, not declared: **effective capacity = availability − approved
leave.** No one fills in a fantasy capacity number; the pieces come from real availability settings
and approved time off.

![capacity planner](/shots/capacity.png)

## The formula

```
effective capacity = availability − approved leave
```

Only **approved** leave subtracts. Pending or rejected leave does not reduce capacity, so a request
in flight never silently changes the plan.

```http
GET /api/capacity
```

## Availability

Each member has working hours and days per week. The default is 40 weekly hours.

| Field | Meaning |
|---|---|
| `weekly_hours` | hours per week the member is available (`> 0`, `≤ 168`) |
| `working_days` | which days of the week count |
| `effective_from` | availability is dated, so changes don't rewrite history |

```http
GET /api/availability
PUT /api/availability
```

## PTO / leave

Leave entries feed directly into capacity once approved.

| Field | Values |
|---|---|
| `kind` | `pto` · `sick` · `holiday` |
| `status` | `pending` · `approved` · `rejected` |
| `start_date` / `end_date` | the leave window |

```http
GET   /api/leave              # list
POST  /api/leave              # request leave (starts pending)
PATCH /api/leave/{id}         # approve / reject
```

Only entries that **overlap the planning period and are `approved`** subtract from capacity.

## Time tracking

Time entries record minutes against an issue. The `source` distinguishes how the time was captured:

| `source` | Meaning |
|---|---|
| git-derived | inferred from git activity for developers (the derived-not-entered path) |
| manual | entered by a human where git can't see the work |

```http
GET  /api/time-entries
POST /api/time-entries
```

This mirrors the [evidence-with-visible-gaps](/docs/concepts) discipline: dev time is derived from
git where possible; everything else is explicit, manual, and visible — never fabricated. The same
distinction drives evidence [billing](/docs/billing).
