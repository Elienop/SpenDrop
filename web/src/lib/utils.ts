import { clsx, type ClassValue } from 'clsx';
import type { FocusEvent } from 'react';
import { twMerge } from 'tailwind-merge';

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}

/**
 * Select the current input's value on focus so the user can
 * overwrite pre-filled content (especially placeholder `0`s)
 * just by typing. Intended for numeric inputs.
 */
export function selectAllOnFocus(e: FocusEvent<HTMLInputElement>) {
  e.currentTarget.select();
}
