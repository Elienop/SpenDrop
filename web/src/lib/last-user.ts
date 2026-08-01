import { STORAGE_KEYS } from '@/lib/storage-keys';
import type { User } from '@/api/types';

/**
 * The last identity the SERVER confirmed on this device.
 *
 * Why this exists: `GET /auth/me` is the only thing that can tell the app who
 * is signed in, and it cannot answer with no signal. Without a remembered
 * identity the app has to assume "nobody", which redirects to /login — the one
 * screen that is useless offline — and takes the offline capture screen down
 * with it, in exactly the situation capture was built for.
 *
 * What is deliberately NOT stored:
 *  - No token, no password, nothing that authenticates anything. The session
 *    cookie remains the only credential; this is a name, not a key.
 *  - No role. A remembered identity is restored as a plain member, so nothing
 *    the server has not confirmed can widen what the UI offers. The real role
 *    arrives with the next successful /auth/me.
 *
 * The value is only ever a HINT: everything read from it stays flagged
 * unverified until the server confirms it (see useAuth), reads stay gated, and
 * queued writes are never replayed under it.
 */
export interface RememberedUser {
  id: number;
  username: string;
  display_name: string;
  created_at: string;
}

function isRememberedUser(value: unknown): value is RememberedUser {
  if (typeof value !== 'object' || value === null) return false;
  const v = value as Record<string, unknown>;
  return (
    typeof v.id === 'number' &&
    Number.isInteger(v.id) &&
    v.id > 0 &&
    typeof v.username === 'string' &&
    typeof v.display_name === 'string' &&
    typeof v.created_at === 'string'
  );
}

export function rememberUser(user: User): void {
  try {
    localStorage.setItem(
      STORAGE_KEYS.lastUser,
      JSON.stringify({
        id: user.id,
        username: user.username,
        display_name: user.display_name,
        created_at: user.created_at,
      } satisfies RememberedUser),
    );
  } catch {
    // Storage unavailable (private mode / quota). Offline capture will simply
    // be unreachable on this device; nothing else depends on it.
  }
}

export function readRememberedUser(): RememberedUser | null {
  try {
    const raw = localStorage.getItem(STORAGE_KEYS.lastUser);
    if (!raw) return null;
    const parsed: unknown = JSON.parse(raw);
    // Hand-edited or half-written storage must not become a user object.
    return isRememberedUser(parsed) ? parsed : null;
  } catch {
    return null;
  }
}

export function forgetRememberedUser(): void {
  try {
    localStorage.removeItem(STORAGE_KEYS.lastUser);
  } catch {
    // Nothing to do — the value is a hint, and every use of it is re-checked
    // against the server.
  }
}

/**
 * Rebuild a `User` from the remembered hint, at the LOWEST privilege. Callers
 * must pair this with the `unverified` flag — the object looks like a normal
 * user on purpose (so the capture screen needs no special-casing), and the
 * flag is what stops it from being trusted.
 */
export function toUnverifiedUser(remembered: RememberedUser): User {
  return {
    id: remembered.id,
    username: remembered.username,
    display_name: remembered.display_name,
    created_at: remembered.created_at,
    role: 'member',
  };
}
