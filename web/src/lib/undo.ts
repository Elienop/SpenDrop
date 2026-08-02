/**
 * How long a delete's "Undo" toast stays up, shared by every
 * delete-with-undo surface (desktop transaction rows, the phone capture
 * panel) so the recovery window feels identical everywhere. Longer than
 * sonner's default because for a pending (offline) row the toast is the
 * ONLY way back, and for saved rows it is the fastest one.
 */
export const UNDO_TOAST_MS = 10000;
