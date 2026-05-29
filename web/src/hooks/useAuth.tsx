import {
  createContext,
  useContext,
  useState,
  useEffect,
  useCallback,
} from 'react';
import type { ReactNode } from 'react';
import { useNavigate } from 'react-router-dom';
import { api } from '../api/client';
import { purgeQueue } from '@/lib/offline-queue';
import type { User } from '../api/types';

interface AuthContextType {
  user: User | null;
  loading: boolean;
  login: (username: string, password: string) => Promise<void>;
  register: (
    username: string,
    password: string,
    displayName: string,
  ) => Promise<void>;
  logout: () => Promise<void>;
}

const AuthContext = createContext<AuthContextType | null>(null);

export function AuthProvider({ children }: { children: ReactNode }) {
  const [user, setUser] = useState<User | null>(null);
  const [loading, setLoading] = useState(true);
  const navigate = useNavigate();

  useEffect(() => {
    api
      .get<User>('auth/me')
      .then((data) => {
        setUser(data);
      })
      .catch(() => {
        setUser(null);
      })
      .finally(() => {
        setLoading(false);
      });
  }, []);

  const login = useCallback(
    async (username: string, password: string) => {
      const data = await api.post<User>('auth/login', {
        username,
        password,
      });
      setUser(data);
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
      setUser(data);
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
      setUser(null);
    }
    navigate('/login');
  }, [navigate, user]);

  return (
    <AuthContext.Provider value={{ user, loading, login, register, logout }}>
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
