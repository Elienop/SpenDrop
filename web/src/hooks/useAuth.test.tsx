import { describe, test, expect, beforeEach, afterEach, vi } from 'vitest';
import { act, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';

// Mock the API client module. The real ApiError / NetworkError classes are
// kept: the whole point of the bootstrap is that it discriminates between
// them, so a stubbed error class would test nothing.
vi.mock('../api/client', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../api/client')>();
  return {
    ...actual,
    api: {
      get: vi.fn(),
      post: vi.fn(),
      del: vi.fn(),
    },
  };
});

// Logout purges the leaving user's per-user offline queue. Mock the lib so the
// test asserts the call without touching IndexedDB.
const purgeQueue = vi.fn();
const markNeedsSignIn = vi.fn();
const clearNeedsSignIn = vi.fn();
vi.mock('@/lib/offline-queue', () => ({
  purgeQueue: (...args: unknown[]) => purgeQueue(...args),
  markNeedsSignIn: (...args: unknown[]) => markNeedsSignIn(...args),
  clearNeedsSignIn: (...args: unknown[]) => clearNeedsSignIn(...args),
}));

// We'll import after mock setup
import { api, ApiError, NetworkError } from '../api/client';
import { AuthProvider, useAuth } from './useAuth';
import { readRememberedUser, rememberUser } from '@/lib/last-user';
import type { User } from '@/api/types';
import { pushTestState, makeSubscription } from '@/test/setup';

const mockedApi = vi.mocked(api);

// Test component that exposes auth context values
function AuthDisplay() {
  const { user, loading, unverified, login, logout, register, refreshUser } =
    useAuth();
  return (
    <div>
      <span data-testid="loading">{String(loading)}</span>
      <span data-testid="unverified">{String(unverified)}</span>
      <span data-testid="role">{user ? user.role : 'none'}</span>
      <span data-testid="user">{user ? user.display_name : 'null'}</span>
      <button onClick={() => login('alice', 'pass123')}>Login</button>
      <button onClick={() => register('bob', 'pass456', 'Bob')}>
        Register
      </button>
      <button onClick={() => logout()}>Logout</button>
      <button onClick={() => void refreshUser()}>Refresh</button>
    </div>
  );
}

function renderWithProviders(ui: React.ReactElement) {
  return render(
    <MemoryRouter>
      <AuthProvider>{ui}</AuthProvider>
    </MemoryRouter>,
  );
}

// The Cache Storage API is absent in happy-dom; stub a deletable spy so the
// logout purge path has something to call.
const cachesDelete = vi.fn().mockResolvedValue(true);

// EventSource test double (happy-dom ships none). useLiveUpdates opens one when
// authenticated; logout sets user→null, whose effect cleanup must close it.
const esClose = vi.fn();
class LogoutMockEventSource {
  static instances: LogoutMockEventSource[] = [];
  onopen: ((ev: Event) => void) | null = null;
  onmessage: ((ev: MessageEvent) => void) | null = null;
  onerror: ((ev: Event) => void) | null = null;
  close = esClose;
  // useLiveUpdates subscribes via addEventListener('invalidate', …) (named SSE
  // event), so the stub must provide these or the hook throws on mount.
  addEventListener = vi.fn();
  removeEventListener = vi.fn();
  constructor() {
    LogoutMockEventSource.instances.push(this);
  }
}

// Real shared QueryClient stub so useLiveUpdates' invalidate calls no-op.
vi.mock('@/lib/queryClient', () => ({
  queryClient: { invalidateQueries: vi.fn().mockResolvedValue(undefined) },
}));

/**
 * Let React flush the passive effect scheduled by the render this test just
 * observed in the DOM.
 *
 * `waitFor` can resolve one macrotask BEFORE that effect runs: happy-dom
 * delivers MutationObserver records on the microtask queue, so the assertion is
 * satisfied by the commit itself, while React's passive-effect flush is a
 * separate scheduler task. An event dispatched into that gap reaches no
 * listener and is gone for good — there is no retry. Measured margin without
 * this call is exactly one event-loop turn, which a loaded CI worker loses.
 */
async function flushPendingEffects(): Promise<void> {
  await act(async () => {});
}

describe('useAuth', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    // `clearAllMocks` only wipes call records — it does NOT drain the queues
    // built by mockResolvedValueOnce/mockRejectedValueOnce (only `mockReset`
    // does). A response queued by one test and never consumed would be handed
    // to the NEXT test's `/auth/me`, shifting every later test in this file by
    // one response and turning a single failure into a file-wide cascade of
    // nonsensical ones.
    mockedApi.get.mockReset();
    mockedApi.post.mockReset();
    mockedApi.del.mockReset();
    localStorage.clear();
    purgeQueue.mockResolvedValue(undefined);
    cachesDelete.mockResolvedValue(true);
    vi.stubGlobal('caches', { delete: cachesDelete });
    pushTestState.permission = 'default';
    pushTestState.subscription = null;
  });

  afterEach(() => {
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
    pushTestState.subscription = null;
  });

  test('starts in loading state and checks session on mount', async () => {
    mockedApi.get.mockResolvedValueOnce({
      id: 1,
      username: 'alice',
      display_name: 'Alice',
      role: 'admin',
      created_at: '2024-01-01',
    });

    renderWithProviders(<AuthDisplay />);

    // Initially loading
    expect(screen.getByTestId('loading')).toHaveTextContent('true');

    // After session check resolves
    await waitFor(() => {
      expect(screen.getByTestId('loading')).toHaveTextContent('false');
    });
    expect(screen.getByTestId('user')).toHaveTextContent('Alice');
    expect(mockedApi.get).toHaveBeenCalledWith('auth/me');
  });

  // The bootstrap's two failure modes are NOT the same event and must not be
  // handled the same way. Previously this was one test rejecting with a bare
  // `new Error('Unauthorized')` — which passes whether or not the code can
  // tell them apart, so it pinned the bug instead of catching it.
  test('signs the user out when the session check gets a real 401', async () => {
    mockedApi.get.mockRejectedValueOnce(new ApiError('Unauthorized', 401));

    renderWithProviders(<AuthDisplay />);

    await waitFor(() => {
      expect(screen.getByTestId('loading')).toHaveTextContent('false');
    });
    expect(screen.getByTestId('user')).toHaveTextContent('null');
    expect(screen.getByTestId('unverified')).toHaveTextContent('false');
  });

  test('does NOT sign the user out when the session check never reached the server', async () => {
    // Drove out of an underground car park: the request failed in transit, the
    // server never said anything. Signing out here is the "logged out when I
    // left the house" bug.
    rememberUser({
      id: 5,
      username: 'alice',
      display_name: 'Alice',
      role: 'admin',
      created_at: '2024-01-01',
    });
    mockedApi.get.mockRejectedValueOnce(
      new NetworkError('The device is offline', 'offline'),
    );

    renderWithProviders(<AuthDisplay />);

    await waitFor(() => {
      expect(screen.getByTestId('loading')).toHaveTextContent('false');
    });
    expect(screen.getByTestId('user')).toHaveTextContent('Alice');
    expect(screen.getByTestId('unverified')).toHaveTextContent('true');
  });

  test('a 5xx from /auth/me is not a sign-out either', async () => {
    rememberUser({
      id: 5,
      username: 'alice',
      display_name: 'Alice',
      role: 'admin',
      created_at: '2024-01-01',
    });
    mockedApi.get.mockRejectedValueOnce(new ApiError('bad gateway', 502));

    renderWithProviders(<AuthDisplay />);

    await waitFor(() => {
      expect(screen.getByTestId('loading')).toHaveTextContent('false');
    });
    expect(screen.getByTestId('user')).toHaveTextContent('Alice');
    expect(screen.getByTestId('unverified')).toHaveTextContent('true');
  });

  test('falls back to no user when the request fails and nobody is remembered', async () => {
    mockedApi.get.mockRejectedValueOnce(
      new NetworkError('The device is offline', 'offline'),
    );

    renderWithProviders(<AuthDisplay />);

    await waitFor(() => {
      expect(screen.getByTestId('loading')).toHaveTextContent('false');
    });
    expect(screen.getByTestId('user')).toHaveTextContent('null');
    expect(screen.getByTestId('unverified')).toHaveTextContent('false');
  });

  test('an unverified identity carries no elevated role', async () => {
    rememberUser({
      id: 5,
      username: 'alice',
      display_name: 'Alice',
      role: 'admin',
      created_at: '2024-01-01',
    });
    mockedApi.get.mockRejectedValueOnce(
      new NetworkError('The device is offline', 'offline'),
    );

    renderWithProviders(<AuthDisplay />);

    await waitFor(() => {
      expect(screen.getByTestId('user')).toHaveTextContent('Alice');
    });
    // Nothing the server has not confirmed may grant admin surface area.
    expect(screen.getByTestId('role')).toHaveTextContent('member');
  });

  // Recovery must not be gated behind the state that broke. When signal comes
  // back the app has to heal by itself — the owner should never have to
  // force-quit the PWA.
  test('re-verifies by itself when connectivity returns', async () => {
    rememberUser({
      id: 5,
      username: 'alice',
      display_name: 'Alice',
      role: 'admin',
      created_at: '2024-01-01',
    });
    mockedApi.get.mockRejectedValueOnce(
      new NetworkError('The device is offline', 'offline'),
    );

    renderWithProviders(<AuthDisplay />);

    await waitFor(() => {
      expect(screen.getByTestId('unverified')).toHaveTextContent('true');
    });
    await flushPendingEffects();

    mockedApi.get.mockResolvedValueOnce({
      id: 5,
      username: 'alice',
      display_name: 'Alice',
      role: 'admin',
      created_at: '2024-01-01',
    });

    window.dispatchEvent(new Event('online'));

    await waitFor(() => {
      expect(screen.getByTestId('unverified')).toHaveTextContent('false');
    });
    expect(screen.getByTestId('role')).toHaveTextContent('admin');
    expect(mockedApi.get).toHaveBeenCalledTimes(2);
  });

  test('re-verifies when the app is brought back to the foreground', async () => {
    rememberUser({
      id: 5,
      username: 'alice',
      display_name: 'Alice',
      role: 'admin',
      created_at: '2024-01-01',
    });
    mockedApi.get.mockRejectedValueOnce(
      new NetworkError('Could not reach the server', 'unreachable'),
    );

    renderWithProviders(<AuthDisplay />);

    await waitFor(() => {
      expect(screen.getByTestId('unverified')).toHaveTextContent('true');
    });
    await flushPendingEffects();

    mockedApi.get.mockResolvedValueOnce({
      id: 5,
      username: 'alice',
      display_name: 'Alice',
      role: 'admin',
      created_at: '2024-01-01',
    });
    document.dispatchEvent(new Event('visibilitychange'));

    await waitFor(() => {
      expect(screen.getByTestId('unverified')).toHaveTextContent('false');
    });
  });

  test('a verified session is remembered so the capture screen survives a cold start offline', async () => {
    mockedApi.get.mockResolvedValueOnce({
      id: 12,
      username: 'alice',
      display_name: 'Alice',
      role: 'admin',
      created_at: '2024-01-01',
    });

    renderWithProviders(<AuthDisplay />);

    await waitFor(() => {
      expect(screen.getByTestId('user')).toHaveTextContent('Alice');
    });
    expect(readRememberedUser()).toMatchObject({ id: 12, display_name: 'Alice' });
    // A confirmed session releases any hold left by an earlier expiry.
    expect(clearNeedsSignIn).toHaveBeenCalledWith(12);
  });

  test('a real 401 holds the remembered user\'s queued rows for sign-in instead of purging them', async () => {
    rememberUser({
      id: 5,
      username: 'alice',
      display_name: 'Alice',
      role: 'member',
      created_at: '2024-01-01',
    });
    mockedApi.get.mockRejectedValueOnce(new ApiError('Unauthorized', 401));

    renderWithProviders(<AuthDisplay />);

    await waitFor(() => {
      expect(screen.getByTestId('user')).toHaveTextContent('null');
    });
    // Held, never discarded, and never re-filed under whoever signs in next.
    expect(markNeedsSignIn).toHaveBeenCalledWith(5);
    expect(purgeQueue).not.toHaveBeenCalled();
    expect(readRememberedUser()).toBeNull();
  });

  test('holds the previous person\'s captures when the device turns out to belong to someone else', async () => {
    rememberUser({
      id: 5,
      username: 'alice',
      display_name: 'Alice',
      role: 'member',
      created_at: '2024-01-01',
    });
    mockedApi.get.mockResolvedValueOnce({
      id: 6,
      username: 'bob',
      display_name: 'Bob',
      role: 'member',
      created_at: '2024-01-01',
    });

    renderWithProviders(<AuthDisplay />);

    await waitFor(() => {
      expect(screen.getByTestId('user')).toHaveTextContent('Bob');
    });
    // Alice's rows are held, not replayed through Bob's session.
    expect(markNeedsSignIn).toHaveBeenCalledWith(5);
    expect(clearNeedsSignIn).toHaveBeenCalledWith(6);
  });

  test('a transport failure does not hold the queue', async () => {
    rememberUser({
      id: 5,
      username: 'alice',
      display_name: 'Alice',
      role: 'member',
      created_at: '2024-01-01',
    });
    mockedApi.get.mockRejectedValueOnce(
      new NetworkError('The device is offline', 'offline'),
    );

    renderWithProviders(<AuthDisplay />);

    await waitFor(() => {
      expect(screen.getByTestId('loading')).toHaveTextContent('false');
    });
    expect(markNeedsSignIn).not.toHaveBeenCalled();
  });

  // The shell (Sidebar, MobileNav) renders `user.display_name` straight from
  // this context, and no SSE event fires when a user row is PUT. So renaming
  // yourself in Settings has to move this value, or the name in the corner of
  // the app stays wrong until a reload — which is the primary flow, since the
  // reason the editor exists is an admin shortening their OWN name.
  test('refreshUser adopts a new display name without a reload', async () => {
    mockedApi.get.mockResolvedValueOnce({
      id: 1,
      username: 'alice',
      display_name: 'Alexandra Abdelahad',
      role: 'admin',
      created_at: '2024-01-01',
    });

    const user = userEvent.setup();
    renderWithProviders(<AuthDisplay />);

    await waitFor(() => {
      expect(screen.getByTestId('user')).toHaveTextContent(
        'Alexandra Abdelahad',
      );
    });

    mockedApi.get.mockResolvedValueOnce({
      id: 1,
      username: 'alice',
      display_name: 'Ellie',
      role: 'admin',
      created_at: '2024-01-01',
    });
    await user.click(screen.getByText('Refresh'));

    // Anchored, and against a name that is not a substring of the old one.
    // `toHaveTextContent('Alex')` was the first version of this line and it
    // passed with `setUser` DELETED, because it matches a substring and
    // "Alexandra Abdelahad" contains "Alex" — the assertion was reading the
    // stale name and calling it the new one.
    await waitFor(() => {
      expect(screen.getByTestId('user')).toHaveTextContent(/^Ellie$/);
    });
    // The remembered identity carries display_name too — without this the next
    // cold start offline would put the old name back on screen.
    expect(readRememberedUser()).toMatchObject({
      id: 1,
      display_name: 'Ellie',
    });
  });

  // The reason this is its own function rather than a re-run of verifySession.
  // A cosmetic re-read must never answer the question "who are you": flipping
  // `unverified` here would send ProtectedRoute to the sign-in screen, throwing
  // an admin off the page they were on right after a save that SUCCEEDED.
  test('a refresh that never reaches the server changes nothing', async () => {
    mockedApi.get.mockResolvedValueOnce({
      id: 1,
      username: 'alice',
      display_name: 'Alice',
      role: 'admin',
      created_at: '2024-01-01',
    });

    const user = userEvent.setup();
    renderWithProviders(<AuthDisplay />);

    await waitFor(() => {
      expect(screen.getByTestId('user')).toHaveTextContent('Alice');
    });

    mockedApi.get.mockRejectedValueOnce(
      new NetworkError('The device is offline', 'offline'),
    );
    await user.click(screen.getByText('Refresh'));
    await flushPendingEffects();

    expect(screen.getByTestId('user')).toHaveTextContent('Alice');
    expect(screen.getByTestId('unverified')).toHaveTextContent('false');
    expect(screen.getByTestId('loading')).toHaveTextContent('false');
  });

  // A 200 from /auth/me IS the server confirming this session, so the success
  // arm has to clear `unverified` — every other writer of `user` pairs the two,
  // and leaving them disagreeing means a confirmed session still reads as
  // unverified, which gates reads and writes it should not gate. The role
  // assertion is the second half: an unverified identity is restored as a plain
  // member, so the real role coming back proves the user object was replaced.
  test('a successful refresh clears the unverified flag', async () => {
    rememberUser({
      id: 5,
      username: 'alice',
      display_name: 'Alice',
      role: 'admin',
      created_at: '2024-01-01',
    });
    mockedApi.get.mockRejectedValueOnce(
      new NetworkError('The device is offline', 'offline'),
    );

    const user = userEvent.setup();
    renderWithProviders(<AuthDisplay />);

    await waitFor(() => {
      expect(screen.getByTestId('unverified')).toHaveTextContent('true');
    });
    await flushPendingEffects();
    expect(screen.getByTestId('role')).toHaveTextContent('member');

    mockedApi.get.mockResolvedValueOnce({
      id: 5,
      username: 'alice',
      display_name: 'Alice',
      role: 'admin',
      created_at: '2024-01-01',
    });
    await user.click(screen.getByText('Refresh'));

    await waitFor(() => {
      expect(screen.getByTestId('unverified')).toHaveTextContent('false');
    });
    expect(screen.getByTestId('role')).toHaveTextContent('admin');
  });

  // The race that made this a generation counter rather than a plain re-read.
  // A /auth/me can still be outstanding when logout finishes — logout awaits a
  // POST, push teardown, a queue purge and a cache delete, which is a wide
  // window — and its answer describes the session that has just ended. Written
  // back, it re-remembers the departed identity and renders them signed in
  // again, and the module-singleton queryClient (which logout does not clear)
  // then serves their cached household data to whoever is holding the device.
  test('a refresh in flight when logout completes does not resurrect the session', async () => {
    mockedApi.get.mockResolvedValueOnce({
      id: 1,
      username: 'alice',
      display_name: 'Alice',
      role: 'admin',
      created_at: '2024-01-01',
    });
    mockedApi.post.mockResolvedValueOnce(undefined);

    const user = userEvent.setup();
    renderWithProviders(<AuthDisplay />);

    await waitFor(() => {
      expect(screen.getByTestId('user')).toHaveTextContent(/^Alice$/);
    });

    // A /auth/me that will not answer until this test says so.
    let answerRefresh: ((value: User) => void) | undefined;
    const parked = new Promise<User>((resolve) => {
      answerRefresh = resolve;
    });
    mockedApi.get.mockReturnValueOnce(parked);

    await user.click(screen.getByText('Refresh'));
    await user.click(screen.getByText('Logout'));

    await waitFor(() => {
      expect(screen.getByTestId('user')).toHaveTextContent(/^null$/);
    });
    expect(readRememberedUser()).toBeNull();

    // The late answer lands, describing a session that no longer exists here.
    await act(async () => {
      answerRefresh?.({
        id: 1,
        username: 'alice',
        display_name: 'Alice',
        role: 'admin',
        created_at: '2024-01-01',
      });
      await parked;
    });

    expect(screen.getByTestId('user')).toHaveTextContent(/^null$/);
    expect(readRememberedUser()).toBeNull();
  });

  // The guard is on verifySession's success arm too, and this is the test that
  // keeps that line honest — a backstop nothing exercises is indistinguishable
  // from a line that does nothing. Reaching it needs the reconnect retry rather
  // than the mount call, because mount resolves before any sign-out is
  // reachable. Whether a real user can hit this today is doubtful (an
  // unverified session is redirected to the sign-in screen, which has no Log
  // out button); it is guarded because it is the same shape as the bug above,
  // and one line beats reasoning about reachability every time somebody edits
  // this file.
  test('a reconnect re-verify in flight when logout completes does not resurrect the session', async () => {
    rememberUser({
      id: 5,
      username: 'alice',
      display_name: 'Alice',
      role: 'admin',
      created_at: '2024-01-01',
    });
    mockedApi.get.mockRejectedValueOnce(
      new NetworkError('The device is offline', 'offline'),
    );

    const user = userEvent.setup();
    renderWithProviders(<AuthDisplay />);

    await waitFor(() => {
      expect(screen.getByTestId('unverified')).toHaveTextContent('true');
    });
    await flushPendingEffects();

    let answerVerify: ((value: User) => void) | undefined;
    const parked = new Promise<User>((resolve) => {
      answerVerify = resolve;
    });
    mockedApi.get.mockReturnValueOnce(parked);
    mockedApi.post.mockResolvedValueOnce(undefined);

    await act(async () => {
      window.dispatchEvent(new Event('online'));
    });
    await user.click(screen.getByText('Logout'));

    await waitFor(() => {
      expect(screen.getByTestId('user')).toHaveTextContent(/^null$/);
    });

    await act(async () => {
      answerVerify?.({
        id: 5,
        username: 'alice',
        display_name: 'Alice',
        role: 'admin',
        created_at: '2024-01-01',
      });
      await parked;
    });

    expect(screen.getByTestId('user')).toHaveTextContent(/^null$/);
    expect(readRememberedUser()).toBeNull();
  });

  /**
   * Park a re-verify in flight, then complete a sign-in over the top of it.
   *
   * Alice is remembered-but-unconfirmed (an offline cold start — the state
   * ProtectedRoute sends to /login). Connectivity returns, the self-healing
   * effect fires verifySession, and its /auth/me hangs on a slow link. Bob then
   * signs in successfully on the same device, as an ADMIN so that a role
   * downgrade is visible. The returned handle makes the parked request FAIL,
   * which is the moment the unguarded catch used to write over Bob.
   *
   * `action` picks which entry point completes the sign-in: login and register
   * bump the epoch identically, and each bump needs its own test or a mutant
   * that removes just one of them survives.
   */
  async function parkReverifyThenSignIn(action: 'Login' | 'Register'): Promise<{
    failReverify: (e: unknown) => Promise<void>;
  }> {
    rememberUser({
      id: 5,
      username: 'alice',
      display_name: 'Alice',
      role: 'admin',
      created_at: '2024-01-01',
    });
    mockedApi.get.mockRejectedValueOnce(
      new NetworkError('The device is offline', 'offline'),
    );

    const user = userEvent.setup();
    renderWithProviders(<AuthDisplay />);

    await waitFor(() => {
      expect(screen.getByTestId('unverified')).toHaveTextContent('true');
    });
    await flushPendingEffects();

    let reject: ((e: unknown) => void) | undefined;
    const parked = new Promise<User>((_resolve, rej) => {
      reject = rej;
    });
    // Claim the rejection now: an unhandled one fails the run on its own,
    // which would look like the assertion failing.
    parked.catch(() => {});
    mockedApi.get.mockReturnValueOnce(parked);

    await act(async () => {
      window.dispatchEvent(new Event('online'));
    });

    mockedApi.post.mockResolvedValueOnce({
      id: 9,
      username: 'bob',
      display_name: 'Bob',
      role: 'admin',
      created_at: '2024-02-02',
    });
    await user.click(screen.getByText(action));

    // Preconditions, all load-bearing: Bob is fully signed in and CONFIRMED
    // before the late failure lands, so anything that changes afterwards was
    // written by the stale request rather than by the sign-in itself. And the
    // re-verify really is still outstanding — two /auth/me calls, one parked.
    await waitFor(() => {
      expect(screen.getByTestId('user')).toHaveTextContent(/^Bob$/);
    });
    expect(screen.getByTestId('unverified')).toHaveTextContent('false');
    expect(screen.getByTestId('role')).toHaveTextContent('admin');
    expect(mockedApi.get).toHaveBeenCalledTimes(2);

    return {
      failReverify: async (e: unknown) => {
        await act(async () => {
          reject?.(e);
          await parked.catch(() => {});
        });
      },
    };
  }

  // ARM A of the catch. A transport failure falls back to the remembered
  // identity and flags it unverified — correct for its own session, ruinous
  // over somebody else's: `toUnverifiedUser` hardcodes role 'member', so the
  // admin who just signed in silently loses every admin surface, and
  // `unverified` sends ProtectedRoute back to the sign-in screen.
  test('a transport failure from a stale re-verify does not demote the user who just signed in', async () => {
    const { failReverify } = await parkReverifyThenSignIn('Login');

    const offline = new NetworkError('The device is offline', 'offline');
    // Routes to Arm A only if it is NOT a 401 ApiError. Pinned so this test
    // cannot quietly become a second copy of the one below.
    expect(offline instanceof ApiError).toBe(false);
    await failReverify(offline);

    expect(screen.getByTestId('user')).toHaveTextContent(/^Bob$/);
    expect(screen.getByTestId('unverified')).toHaveTextContent('false');
    expect(screen.getByTestId('role')).toHaveTextContent('admin');
  });

  // ARM B. The 401 is addressed to the session that has already ended, so
  // acting on it signs out the person who just signed in — and files the queue
  // hold against THEIR id, which is how one person's captures end up held under
  // another's name.
  test('a 401 meant for the departed session does not sign the new user out', async () => {
    const { failReverify } = await parkReverifyThenSignIn('Login');

    const unauthorized = new ApiError('unauthorized', 401);
    // ApiError is (message, status). Reversed arguments make `err.status === 401`
    // false and silently route this through Arm A, where the assertions below
    // would still pass — so the fixture's own status is asserted first.
    expect(unauthorized.status).toBe(401);
    await failReverify(unauthorized);

    expect(screen.getByTestId('user')).toHaveTextContent(/^Bob$/);
    expect(readRememberedUser()?.id).toBe(9);
    expect(markNeedsSignIn).not.toHaveBeenCalledWith(9);
  });

  // register bumps the epoch at its own call site. Without a test through that
  // door, removing just that one bump leaves every other test green.
  test('a stale re-verify does not demote a user who just registered', async () => {
    const { failReverify } = await parkReverifyThenSignIn('Register');

    const offline = new NetworkError('Could not reach the server', 'unreachable');
    expect(offline instanceof ApiError).toBe(false);
    await failReverify(offline);

    expect(screen.getByTestId('user')).toHaveTextContent(/^Bob$/);
    expect(screen.getByTestId('unverified')).toHaveTextContent('false');
    expect(screen.getByTestId('role')).toHaveTextContent('admin');
  });

  // Same reasoning, harsher failure: a 401 on this call must not sign anybody
  // out either. `verifySession` stays the only path that may, and it re-runs on
  // mount and on regained connectivity. Someone will read this as a missing
  // case and "fix" it — it is deliberate.
  test('a 401 during a refresh does not sign the user out', async () => {
    mockedApi.get.mockResolvedValueOnce({
      id: 1,
      username: 'alice',
      display_name: 'Alice',
      role: 'admin',
      created_at: '2024-01-01',
    });

    const user = userEvent.setup();
    renderWithProviders(<AuthDisplay />);

    await waitFor(() => {
      expect(screen.getByTestId('user')).toHaveTextContent('Alice');
    });

    mockedApi.get.mockRejectedValueOnce(new ApiError('Unauthorized', 401));
    await user.click(screen.getByText('Refresh'));
    await flushPendingEffects();

    expect(screen.getByTestId('user')).toHaveTextContent('Alice');
    expect(readRememberedUser()).toMatchObject({ id: 1 });
  });

  test('logout forgets the remembered identity', async () => {
    mockedApi.get.mockResolvedValueOnce({
      id: 42,
      username: 'alice',
      display_name: 'Alice',
      role: 'admin',
      created_at: '2024-01-01',
    });
    mockedApi.post.mockResolvedValueOnce(undefined);

    const user = userEvent.setup();
    renderWithProviders(<AuthDisplay />);

    await waitFor(() => {
      expect(screen.getByTestId('user')).toHaveTextContent('Alice');
    });
    await user.click(screen.getByText('Logout'));

    await waitFor(() => {
      expect(screen.getByTestId('user')).toHaveTextContent('null');
    });
    expect(readRememberedUser()).toBeNull();
  });

  test('login calls api and sets user state', async () => {
    // Initial session check fails (not logged in)
    mockedApi.get.mockRejectedValueOnce(new Error('Unauthorized'));
    // Login succeeds
    mockedApi.post.mockResolvedValueOnce({
      id: 1,
      username: 'alice',
      display_name: 'Alice',
      role: 'admin',
      created_at: '2024-01-01',
    });

    const user = userEvent.setup();
    renderWithProviders(<AuthDisplay />);

    await waitFor(() => {
      expect(screen.getByTestId('loading')).toHaveTextContent('false');
    });

    await user.click(screen.getByText('Login'));

    await waitFor(() => {
      expect(screen.getByTestId('user')).toHaveTextContent('Alice');
    });
    expect(mockedApi.post).toHaveBeenCalledWith('auth/login', {
      username: 'alice',
      password: 'pass123',
    });
  });

  test('register calls api and sets user state', async () => {
    mockedApi.get.mockRejectedValueOnce(new Error('Unauthorized'));
    mockedApi.post.mockResolvedValueOnce({
      id: 2,
      username: 'bob',
      display_name: 'Bob',
      role: 'member',
      created_at: '2024-01-01',
    });

    const user = userEvent.setup();
    renderWithProviders(<AuthDisplay />);

    await waitFor(() => {
      expect(screen.getByTestId('loading')).toHaveTextContent('false');
    });

    await user.click(screen.getByText('Register'));

    await waitFor(() => {
      expect(screen.getByTestId('user')).toHaveTextContent('Bob');
    });
    expect(mockedApi.post).toHaveBeenCalledWith('auth/register', {
      username: 'bob',
      password: 'pass456',
      display_name: 'Bob',
    });
  });

  test('logout calls api and clears user state', async () => {
    // Session check returns user
    mockedApi.get.mockResolvedValueOnce({
      id: 1,
      username: 'alice',
      display_name: 'Alice',
      role: 'admin',
      created_at: '2024-01-01',
    });
    // Logout succeeds
    mockedApi.post.mockResolvedValueOnce(undefined);

    const user = userEvent.setup();
    renderWithProviders(<AuthDisplay />);

    await waitFor(() => {
      expect(screen.getByTestId('user')).toHaveTextContent('Alice');
    });

    await user.click(screen.getByText('Logout'));

    await waitFor(() => {
      expect(screen.getByTestId('user')).toHaveTextContent('null');
    });
    expect(mockedApi.post).toHaveBeenCalledWith('auth/logout');
  });

  test('logout clears user state even when the logout POST rejects with 401', async () => {
    // Session check returns a user (we are logged in).
    mockedApi.get.mockResolvedValueOnce({
      id: 1,
      username: 'alice',
      display_name: 'Alice',
      role: 'admin',
      created_at: '2024-01-01',
    });
    // The server already killed the session (e.g. right after a password
    // change), so the logout POST rejects with 401.
    mockedApi.post.mockRejectedValueOnce(new Error('Unauthorized'));

    const user = userEvent.setup();
    renderWithProviders(<AuthDisplay />);

    await waitFor(() => {
      expect(screen.getByTestId('user')).toHaveTextContent('Alice');
    });

    await user.click(screen.getByText('Logout'));

    // Local auth state must still be cleared despite the rejected POST.
    await waitFor(() => {
      expect(screen.getByTestId('user')).toHaveTextContent('null');
    });
    expect(mockedApi.post).toHaveBeenCalledWith('auth/logout');
  });

  test('logout purges the offline queue and api-lists cache for the leaving user', async () => {
    mockedApi.get.mockResolvedValueOnce({
      id: 42,
      username: 'alice',
      display_name: 'Alice',
      role: 'admin',
      created_at: '2024-01-01',
    });
    mockedApi.post.mockResolvedValueOnce(undefined);

    const user = userEvent.setup();
    renderWithProviders(<AuthDisplay />);

    await waitFor(() => {
      expect(screen.getByTestId('user')).toHaveTextContent('Alice');
    });

    await user.click(screen.getByText('Logout'));

    await waitFor(() => {
      expect(screen.getByTestId('user')).toHaveTextContent('null');
    });
    // The leaving user's offline DB and the device-global api-lists Cache are
    // purged so a different account on this device can't replay/read them.
    expect(purgeQueue).toHaveBeenCalledWith(42);
    expect(cachesDelete).toHaveBeenCalledWith('spendrop-api-lists');
  });

  test('logout unsubscribes the device push subscription, DELETEs the server row, and still completes', async () => {
    mockedApi.get.mockResolvedValueOnce({
      id: 7,
      username: 'alice',
      display_name: 'Alice',
      role: 'admin',
      created_at: '2024-01-01',
    });
    mockedApi.post.mockResolvedValueOnce(undefined);
    mockedApi.del.mockResolvedValueOnce(undefined);
    // A push subscription exists on this device, exercising the unsubscribe
    // teardown branch that defaults off (pushTestState.subscription = null).
    const sub = makeSubscription();
    pushTestState.subscription = sub;

    const user = userEvent.setup();
    renderWithProviders(<AuthDisplay />);

    await waitFor(() => {
      expect(screen.getByTestId('user')).toHaveTextContent('Alice');
    });

    await user.click(screen.getByText('Logout'));

    // Teardown ran: the device subscription was unsubscribed and its server row
    // deleted by endpoint, so a different account on this device can't inherit
    // the leaving user's push registration.
    await waitFor(() => {
      expect(sub.unsubscribe).toHaveBeenCalled();
    });
    expect(mockedApi.del).toHaveBeenCalledWith('push/subscriptions', {
      endpoint: sub.endpoint,
    });
    // Logout still completes — the security-critical state clear is not trapped.
    await waitFor(() => {
      expect(screen.getByTestId('user')).toHaveTextContent('null');
    });
  });

  test('logout still purges the queue when the logout POST rejects', async () => {
    mockedApi.get.mockResolvedValueOnce({
      id: 42,
      username: 'alice',
      display_name: 'Alice',
      role: 'admin',
      created_at: '2024-01-01',
    });
    mockedApi.post.mockRejectedValueOnce(new Error('Unauthorized'));

    const user = userEvent.setup();
    renderWithProviders(<AuthDisplay />);

    await waitFor(() => {
      expect(screen.getByTestId('user')).toHaveTextContent('Alice');
    });

    await user.click(screen.getByText('Logout'));

    await waitFor(() => {
      expect(screen.getByTestId('user')).toHaveTextContent('null');
    });
    expect(purgeQueue).toHaveBeenCalledWith(42);
  });

  test('logout tears down the live-updates EventSource via the auth-gated effect', async () => {
    vi.stubGlobal('EventSource', LogoutMockEventSource as unknown as typeof EventSource);
    LogoutMockEventSource.instances = [];
    esClose.mockClear();

    // A small consumer that mounts the live subscriber inside the auth context,
    // so logout (user→null) re-renders it and runs its cleanup.
    const { useLiveUpdates } = await import('./useLiveUpdates');
    function LiveConsumer() {
      useLiveUpdates();
      return null;
    }

    mockedApi.get.mockResolvedValueOnce({
      id: 9,
      username: 'alice',
      display_name: 'Alice',
      role: 'admin',
      created_at: '2024-01-01',
    });
    mockedApi.post.mockResolvedValueOnce(undefined);

    const user = userEvent.setup();
    render(
      <MemoryRouter>
        <AuthProvider>
          <AuthDisplay />
          <LiveConsumer />
        </AuthProvider>
      </MemoryRouter>,
    );

    // Authenticated → exactly one EventSource opened.
    await waitFor(() => {
      expect(screen.getByTestId('user')).toHaveTextContent('Alice');
    });
    expect(LogoutMockEventSource.instances).toHaveLength(1);

    await user.click(screen.getByText('Logout'));

    // user→null re-renders LiveConsumer; the effect cleanup closes the socket.
    await waitFor(() => {
      expect(screen.getByTestId('user')).toHaveTextContent('null');
    });
    expect(esClose).toHaveBeenCalledTimes(1);
  });
});
