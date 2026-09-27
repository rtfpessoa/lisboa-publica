import type {Vehicle} from './api';

// Display retention uses the original source clock, even on disconnected pages.
export function positionDeadline(vehicle: Vehicle): number {
 const deadline = Date.parse(vehicle.observed_at) + 10 * 60 * 1000;
 const published = Date.parse(vehicle.last_known_expires_at ?? '');
 return Number.isFinite(published) ? Math.min(deadline, published) : deadline;
}

// The age warning changes marker/detail presentation, never movement evidence or metrics.
export function positionIsOld(vehicle: Vehicle, now: number): boolean {
 return now - Date.parse(vehicle.observed_at) >= 5 * 60 * 1000;
}
