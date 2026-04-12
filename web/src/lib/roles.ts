// Role enum values, exactly matching the backend's `RoleAdmin` /
// `RoleMember` constants in `internal/api/constants.go`. Any UI check
// against `user.role` should use these constants, not raw strings, so
// that a rename on either side breaks the build rather than silently
// failing at runtime.

export const ROLE_ADMIN = 'admin';
export const ROLE_MEMBER = 'member';

export type Role = typeof ROLE_ADMIN | typeof ROLE_MEMBER;

interface RoleBearer {
  role: Role;
}

/**
 * Returns true if the given user has admin privileges. Accepts `null` /
 * `undefined` so callers can use it directly with `useAuth()`'s `user`
 * field without extra null checks.
 */
export function isAdmin(user: RoleBearer | null | undefined): boolean {
  return user?.role === ROLE_ADMIN;
}
