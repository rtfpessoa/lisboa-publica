import type {Vehicle} from './api';

// Display retention uses the original source clock, even on disconnected pages.
export function positionDeadline(vehicle: Vehicle): number {
 return Date.parse(vehicle.last_known_expires_at ?? '') || Date.parse(vehicle.observed_at) + 24 * 60 * 60 * 1000;
}

// Informational detail only: never use this threshold for membership or metrics.
export function positionIsOld(vehicle: Vehicle, now: number): boolean {
 return now - Date.parse(vehicle.observed_at) >= (vehicle.operator_id === 'cp' ? 10 : 5) * 60 * 1000;
}
