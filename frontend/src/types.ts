// Two layers of types:
//
//   Wire*   — exactly what the Go handlers put on the wire, including the ways
//             encoding/json represents "nothing". Only api.ts should touch these.
//   plain   — the normalized shape the rest of the app consumes, where every
//             collection is guaranteed to be an array and every optional field
//             is explicitly `null`.
//
// The split exists because Go has two different ways of saying "empty" and they
// do not look the same in JSON:
//
//   `[]string`                       nil  ->  null      (key present, value null)
//   `[]T`      with `,omitempty`     nil  ->  <absent>  (key not emitted at all)
//   `*T`       with `,omitempty`     nil  ->  <absent>
//
// See internal/domain/application.go — `location` and `tags` have no omitempty
// so they arrive as null, while `compensation`, `resumes`, and
// `next_follow_up_at` do, so they arrive as undefined. Normalizing both at the
// API boundary is what keeps `app.tags.slice(...)` from throwing in the UI.

export interface User {
  id: string;
  email: string;
  name: string;
  created_at: string;
  updated_at: string;
}

export type ApplicationStatus =
  | "applied"
  | "phone_screen"
  | "onsite"
  | "offer"
  | "rejected"
  | "withdrawn";

export const APPLICATION_STATUSES: readonly ApplicationStatus[] = [
  "applied",
  "phone_screen",
  "onsite",
  "offer",
  "rejected",
  "withdrawn",
];

export function isApplicationStatus(value: string): value is ApplicationStatus {
  return (APPLICATION_STATUSES as readonly string[]).includes(value);
}

export interface CompensationInfo {
  base_salary: number;
  bonus: number;
  equity: string;
  benefits: string[];
}

export interface ResumeVersion {
  id: string;
  label: string;
  file_name: string;
  uploaded_at: string;
}

/** The normalized application the UI works with. Arrays are always arrays. */
export interface Application {
  id: string;
  user_id: string;
  company_name: string;
  position_title: string;
  status: ApplicationStatus;
  location: string[];
  job_link: string;
  job_description: string;
  tags: string[];
  notes: string;
  compensation: CompensationInfo | null;
  resumes: ResumeVersion[];
  applied_at: string;
  next_follow_up_at: string | null;
  created_at: string;
  updated_at: string;
}

/** Raw `domain.Application` as encoding/json emits it. */
export interface WireApplication {
  id: string;
  user_id: string;
  company_name: string;
  position_title: string;
  // `Status` is a plain `string` server-side with the allowed values only in a
  // comment, so the wire type can't assume the union holds.
  status: string;
  location: string[] | null;
  job_link: string;
  job_description: string;
  tags: string[] | null;
  notes: string;
  compensation?: CompensationInfo | null;
  resumes?: ResumeVersion[] | null;
  applied_at: string;
  next_follow_up_at?: string | null;
  created_at: string;
  updated_at: string;
}

/** Raw envelope for `GET /applications` — see API.md. */
export interface WireApplicationPage {
  applications: WireApplication[] | null;
  page: number;
  page_size: number;
  total: number;
}

/** The same page after normalization, in the casing the UI uses. */
export interface ApplicationPage {
  applications: Application[];
  page: number;
  pageSize: number;
  total: number;
}

/** Filters for `GET /applications`. All optional. */
export interface ApplicationQuery {
  status?: ApplicationStatus | "";
  tag?: string;
  search?: string;
  page?: number;
  pageSize?: number;
}

/**
 * The writable fields of an application — the body of POST /applications, and
 * (as a Partial) of PATCH /applications/{id}.
 *
 * Server-managed fields (id, user_id, resumes, created_at, updated_at) are
 * deliberately absent: sending them would be ignored, and including them here
 * would imply the client can set them.
 */
export interface ApplicationInput {
  company_name: string;
  position_title: string;
  status: ApplicationStatus;
  location: string[];
  job_link: string;
  job_description: string;
  tags: string[];
  notes: string;
  compensation: CompensationInfo | null;
  applied_at: string;
  next_follow_up_at: string | null;
}
