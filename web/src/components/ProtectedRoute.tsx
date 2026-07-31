import type { ReactNode } from 'react';
import { Navigate } from 'react-router-dom';
import { useAuth } from '../hooks/useAuth';

interface ProtectedRouteProps {
  children: ReactNode;
  /**
   * Opt this route in for an identity the device remembers but the server has
   * not confirmed (see useAuth's `unverified`). Only the offline capture
   * screen sets it: capture writes to local storage and nothing else, so it
   * is safe without a live session. Every route that READS household data
   * stays gated exactly as before — a remembered name is not a session.
   */
  allowUnverified?: boolean;
}

export function ProtectedRoute({
  children,
  allowUnverified = false,
}: ProtectedRouteProps) {
  const { user, loading, unverified } = useAuth();

  if (loading) {
    return (
      <div
        role="status"
        aria-label="Loading"
        className="flex min-h-screen items-center justify-center"
      >
        <div className="size-8 animate-spin rounded-full border-[3px] border-border border-t-primary" />
      </div>
    );
  }

  if (!user || (unverified && !allowUnverified)) {
    return <Navigate to="/login" replace />;
  }

  return <>{children}</>;
}
