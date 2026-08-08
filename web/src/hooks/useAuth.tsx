import {
  createContext,
  useContext,
  useState,
  useEffect,
  useCallback,
  useRef,
} from 'react';
import type { ReactNode } from 'react';
import { useNavigate } from 'react-router-dom';
import { api, ApiError } from '../api/client';
import {
  purgeQueue,
  markNeedsSignIn,
  clearNeedsSignIn,
} from '@/lib/offline-queue';
import {
  rememberUser,
  readRememberedUser,
  forgetRememberedUser,
  toUnverifiedUser,
} from '@/lib/last-user';
import { getReadyRegistration } from '@/lib/push-sw';
import type { User } from '../api/types';

interface AuthContextType {
  user: User | null;
  loading: boolean;
  /**
   * True when `user` is the identity this device remembers but the server has
   * NOT confirmed it — because `GET /auth/me` never completed, not because it
   * said no. An unverified identity exists so offline capture stays reachable;
   * it must never unlock anything that reads or writes household data. It
   * clears itself the moment the server answers (see `verifySession`).
   */
  unverified: boolean;
  login: (username: string, password: string) => Promise<void>;
  register: (
    username: string,
    password: string,
    displayName: string,
  ) => Promise<void>;
  logout: () => Promise<void>;
  /**
   * Re-read the signed-in user's own profile after something this session did
   * has changed it. Cosmetic only — see the implementation for why it is not
   * the session check.
   */
  refreshUser: () => Promise<void>;
}

const AuthContext = createContext<AuthContextType | null>(null);

export function AuthProvider({ children }: { children: ReactNode }) {
  const [user, setUser] = useState<User | null>(null);
  const [loading, setLoading] = useState(true);
  const [unverified, setUnverified] = useState(false);
  const navigate = useNavigate();

  /**
   * Which identity this provider currently represents, as a counter.
   *
   * Every deliberate change of identity — sign-in, sign-up, sign-out — bumps
   * it. A request that started before the bump is answering about a session
   * that no longer exists here, so its result must be DISCARDED rather than
   * written back. Without this, a `/auth/me` still in flight when logout
   * finishes lands afterwards and undoes the teardown below: it re-remembers
   * the departed identity and re-renders them as signed in, and because the
   * shared queryClient is a module singleton that logout does not clear, their
   * cached household data comes back with them.
   *
   * A ref, not state: it is read inside async closures that must see the
   * CURRENT value rather than the one captured when they were created, and
   * bumping it must never schedule a render of its own.
   */
  const sessionEpoch = useRef(0);

  /**
   * Ask the server who we are, and translate the answer honestly.
   *
   * The load-bearing distinction: a 401 is the SERVER saying "you are not
   * signed in". Anything else — a dropped radio, a timeout, a 502 from the
   * reverse proxy — is the server saying nothing at all, and must never be
   * presented to the user as a sign-out. Catching with zero arity (the
   * original `.catch(() => setUser(null))`) collapsed the two, which is why
   * driving out of a car park signed the owner out of a session that was
   * never cancelled.
   */
  const verifySession = useCallback(async (): Promise<void> => {
    const epoch = sessionEpoch.current;
    try {
      const data = await api.get<User>('auth/me');
      // Same in-flight hazard as refreshUser, and the same answer: if the
      // identity changed while this was outstanding, this answer is about the
      // old one. Only the SUCCESS arm is guarded — the failure arms either
      // clear state (harmless to repeat after a sign-out) or fall back to the
      // remembered identity, which a sign-out has already forgotten. Returning
      // here still runs the `finally`, so `loading` is released either way and
      // the app can never hang on the splash screen.
      if (epoch !== sessionEpoch.current) return;
      const previous = readRememberedUser();
      if (previous && previous.id !== data.id) {
        // This device now belongs to somebody else. Anything the previous
        // person captured stays in THEIR queue, explicitly held — never
        // stranded silently, and never posted through this session.
        markNeedsSignIn(previous.id);
      }
      rememberUser(data);
      // A confirmed session releases any hold an earlier expiry left on this
      // user's queued captures.
      clearNeedsSignIn(data.id);
      setUser(data);
      setUnverified(false);
    } catch (err) {
      if (err instanceof ApiError && err.status === 401) {
        // The server answered, and the answer was no.
        const remembered = readRememberedUser();
        if (remembered) {
          // The captures are NOT discarded and NOT replayed: they stay in this
          // user's own database, held until that same person signs back in.
          // Draining them under whatever session the device gets next is how a
          // row ends up filed under the wrong household member.
          markNeedsSignIn(remembered.id);
        }
        forgetRememberedUser();
        setUser(null);
        setUnverified(false);
      } else {
        // No answer. Fall back to the identity this device last had confirmed
        // so offline capture is reachable, and flag it as unverified.
        const remembered = readRememberedUser();
        setUser(remembered ? toUnverifiedUser(remembered) : null);
        setUnverified(remembered !== null);
      }
    } finally {
      setLoading(false);
    }
  }, []);

  /**
   * Re-read this session's own profile because something the app just did
   * changed it. The case it exists for: an admin renames themselves in
   * Settings, and the shell renders `user.display_name` — no SSE event fires
   * on a user PUT, so nothing else would correct it before a reload.
   *
   * Deliberately NOT `verifySession`. That one is the session CHECK, and its
   * failure modes are answers about WHO YOU ARE: a real 401 signs the user
   * out, and anything else flags them `unverified`, which ProtectedRoute turns
   * into a redirect away from the page they were on. Routing a cosmetic
   * re-read through that machinery would let one dropped request throw an
   * admin off Settings immediately after a save that SUCCEEDED. So this is
   * inert when the request fails — the name stays stale until something else
   * refreshes it, which is how a display detail should degrade — and it never
   * writes `loading`, so nothing unmounts while it runs.
   *
   * On SUCCESS it does clear `unverified`, because a 200 from /auth/me is the
   * server confirming this session — the same pairing every other writer of
   * `user` keeps (verifySession, login, register, logout). Only the failure
   * side is inert.
   */
  const refreshUser = useCallback(async (): Promise<void> => {
    const epoch = sessionEpoch.current;
    try {
      const data = await api.get<User>('auth/me');
      // A sign-out or a sign-in as somebody else completed while this was in
      // flight. See `sessionEpoch`: writing this back would resurrect a
      // session the app has already torn down.
      if (epoch !== sessionEpoch.current) return;
      // The remembered identity carries display_name as well, so without this
      // the next cold start offline would show the old name again.
      rememberUser(data);
      setUser(data);
      setUnverified(false);
    } catch {
      // Inert on purpose — see above.
    }
  }, []);

  useEffect(() => {
    void verifySession();
  }, [verifySession]);

  // Self-healing. Re-ask the server as soon as there is any reason to think it
  // might answer this time — regained connectivity, or the PWA coming back to
  // the foreground (a phone resumed from the background does not always fire
  // 'online'). Without this the app stays stuck in the unverified state until
  // the user force-quits and relaunches it.
  useEffect(() => {
    if (!unverified) return;
    const retry = (): void => {
      void verifySession();
    };
    const onVisible = (): void => {
      if (document.visibilityState !== 'hidden') retry();
    };
    window.addEventListener('online', retry);
    document.addEventListener('visibilitychange', onVisible);
    return () => {
      window.removeEventListener('online', retry);
      document.removeEventListener('visibilitychange', onVisible);
    };
  }, [unverified, verifySession]);

  const login = useCallback(
    async (username: string, password: string) => {
      const data = await api.post<User>('auth/login', {
        username,
        password,
      });
      // A different person may now be signed in on this device; anything the
      // previous session had in flight must not land on top of this one.
      sessionEpoch.current += 1;
      rememberUser(data);
      // Signing back in as the same person releases their held captures, which
      // then drain on the next trigger (see useOfflineSync).
      clearNeedsSignIn(data.id);
      setUser(data);
      setUnverified(false);
      navigate('/');
    },
    [navigate],
  );

  const register = useCallback(
    async (
      username: string,
      password: string,
      displayName: string,
    ) => {
      const data = await api.post<User>('auth/register', {
        username,
        password,
        display_name: displayName,
      });
      // See login: a new identity invalidates whatever the old one had running.
      sessionEpoch.current += 1;
      rememberUser(data);
      clearNeedsSignIn(data.id);
      setUser(data);
      setUnverified(false);
      navigate('/');
    },
    [navigate],
  );

  const logout = useCallback(async () => {
    // Always clear local auth state, even if the logout POST fails. A 401
    // here is routine — e.g. right after a password change the server has
    // already killed this session, so the POST rejects. Swallowing it and
    // clearing state in `finally` keeps the client and server in sync
    // instead of leaving a stale user object behind.
    const uid = user?.id;
    try {
      await api.post('auth/logout');
    } catch {
      // Ignore — the session is gone one way or another.
    } finally {
      // Unsubscribe this device's push subscription and drop the server row
      // BEFORE the offline-queue purge, so a different account logging in here
      // can't inherit the leaving user's push registration. Best-effort —
      // never block logout on push teardown.
      try {
        // getReadyRegistration() resolves to null (never pending) when the SW
        // registration silently failed, so a stuck `ready` can never trap this
        // security-critical teardown — setUser(null)/navigate always run below.
        const reg = await getReadyRegistration();
        if (reg) {
          const sub = await reg.pushManager.getSubscription();
          if (sub) {
            const endpoint = sub.endpoint;
            await sub.unsubscribe();
            await api.del('push/subscriptions', { endpoint });
          }
        }
      } catch {
        // Push may be unsupported / already gone; the server row, if any, is
        // pruned on its next failed send (404/410).
      }
      // Purge this user's offline write-queue and the device-global api-lists
      // Cache so a different account logging in on the same device can never
      // replay or read the leaving user's data. Best-effort — a failure here
      // must not block clearing auth state.
      if (uid !== undefined) {
        try {
          await purgeQueue(uid);
        } catch {
          // Best-effort hygiene; per-user namespacing already isolates replay.
        }
      }
      try {
        await caches.delete('spendrop-api-lists');
      } catch {
        // The Cache Storage API / service worker may be absent (e.g. tests).
      }
      // setUser(null) also tears down the live-updates SSE connection: the
      // EventSource lives inside useLiveUpdates' auth-gated effect, so dropping
      // the user re-runs that effect's cleanup (es.close()). Keep this state
      // clear unconditional — it is the single teardown point for the socket.
      //
      // Retire this identity BEFORE tearing its state down, so that any
      // /auth/me still in flight (a rename refresh, a reconnect re-verify)
      // discards its answer instead of writing the departed user back over the
      // three lines below. The await chain above is exactly the window in
      // which such a response arrives.
      sessionEpoch.current += 1;
      // Forget the remembered identity too: a deliberate sign-out must not
      // leave an offline capture screen reachable for the person who just
      // left. (Their queue was purged just above; the hold goes with it.)
      forgetRememberedUser();
      setUser(null);
      setUnverified(false);
    }
    navigate('/login');
  }, [navigate, user]);

  return (
    <AuthContext.Provider
      value={{ user, loading, unverified, login, register, logout, refreshUser }}
    >
      {children}
    </AuthContext.Provider>
  );
}

export function useAuth(): AuthContextType {
  const context = useContext(AuthContext);
  if (!context) {
    throw new Error('useAuth must be used within AuthProvider');
  }
  return context;
}
