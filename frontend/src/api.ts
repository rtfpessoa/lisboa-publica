/**
 * Lisboa Pública API
 * 1.0.0
 * DO NOT MODIFY - This file has been generated using oazapfts.
 * See https://www.npmjs.com/package/oazapfts
 */
import * as Oazapfts from "@oazapfts/runtime";
import * as QS from "@oazapfts/runtime/query";
export const defaults: Oazapfts.Defaults<Oazapfts.CustomHeaders> = {
    headers: {},
    baseUrl: "/"
};
const oazapfts = Oazapfts.runtime(defaults);
export const servers = {
    server1: "/"
};
export type Health = {
    status: string;
    database: string;
};
export type Error = {
    code: string;
    message: string;
};
export type Config = {
    google_client_id: string | null;
    dev_auth: boolean;
    login_nonce: string;
    history_retention_days: number;
};
export type GoogleLogin = {
    credential: string;
};
export type User = {
    email: string;
    name: string;
    scopes: string[];
    expires_at: string;
    auth_kind: "google" | "development";
};
export type DevLogin = {
    login_nonce: string;
};
export type Operator = {
    id: string;
    name: string;
    color: string;
    mode: "bus" | "metro" | "train" | "ferry";
    static_source: string;
    live_source: string;
    status: "ok" | "stale" | "error" | "loading";
    static_status: "ok" | "stale" | "error" | "loading";
    static_updated_at: string | null;
    live_updated_at: string | null;
    observed_at: string | null;
    plan_id: string | null;
    valid_from: string | null;
    valid_until: string | null;
    reported_positions: number | null;
    estimated_positions: number | null;
    note: string;
    error: string | null;
    static_error: string | null;
    direct_status: ("unconfigured" | "ok" | "error") | null;
    direct_error: string | null;
    direct_updated_at: string | null;
};
export type Page = {
    limit: number;
    offset: number;
    total: number;
    has_more: boolean;
    revision: string | null;
};
export type OperatorPage = {
    data: Operator[];
    page: Page;
};
export type Route = {
    id: string;
    source_id: string;
    operator_id: string;
    short_name: string;
    long_name: string;
    color: string;
    stop_ids: string[];
    plan_id: string | null;
};
export type RoutePage = {
    data: Route[];
    page: Page;
};
export type RouteDetail = Route & {
    geometry: number[][] | null;
};
export type Stop = {
    id: string;
    source_id: string;
    operator_id: string;
    name: string;
    lat: number;
    lon: number;
    parent_id: string | null;
    route_ids: string[];
};
export type StopPage = {
    data: Stop[];
    page: Page;
};
export type Vehicle = {
    id: string;
    source_id: string;
    operator_id: string;
    route_id: string | null;
    route_name: string;
    trip_id: string | null;
    lat: number;
    lon: number;
    observed_at: string;
    collected_at: string;
    position_kind: "reported" | "estimated";
    source_url: string;
    speed_kmh: number | null;
    bearing: number | null;
    model: string | null;
    license_plate: string | null;
    stale: boolean;
    /** Observed source trip plan, retained independently of the active static plan. */
    plan_id: string | null;
};
export type VehiclePage = {
    data: Vehicle[];
    page: Page;
};
export type Trip = {
    id: string;
    operator_id: string;
    route_id: string;
    headsign: string;
    planned_departure: string;
    planned_end: string;
    kind: "scheduled";
};
export type TripPage = {
    data: Trip[];
    page: Page;
};
export type Arrival = {
    id: string;
    operator_id: string;
    stop_id: string;
    route_id: string;
    trip_id: string;
    headsign: string;
    scheduled_at: string | null;
    kind: "scheduled" | "prediction";
    expected_at: string | null;
    observed_at: string | null;
    source_url: string;
};
export type ArrivalPage = {
    data: Arrival[];
    page: Page;
};
export type Metrics = {
    reported_vehicles: number | null;
    estimated_vehicles: number | null;
    speed_kmh: number | null;
    distance_km: number | null;
    detected_trips: number | null;
    first_snapshot: string | null;
    revision: string;
    "from": string;
    to: string;
    unavailable_fields: string[];
};
export type HistoryPoint = {
    bucket: string;
    reported_vehicles: number;
    estimated_vehicles: number;
    speed_kmh: number | null;
    distance_km: number | null;
};
export type HistoryPointPage = {
    data: HistoryPoint[];
    page: Page;
};
export type FleetVehicle = {
    id: string;
    operator_id: string;
    source_id: string;
    model: string | null;
    license_plate: string | null;
    position_kind: "reported" | "estimated";
    first_seen: string;
    last_seen: string;
    distance_km: number | null;
    detected_trips: number | null;
    route_ids: string[];
};
export type FleetVehiclePage = {
    data: FleetVehicle[];
    page: Page;
};
export type TrafficPoint = {
    id: string;
    operator_id: string;
    lat: number;
    lon: number;
    speed_kmh: number;
    observations: number;
};
export type TrafficPointPage = {
    data: TrafficPoint[];
    page: Page;
};
export type Ranking = {
    id: string;
    operator_id: string;
    route_id: string;
    route_name: string;
    reported_vehicles: number;
    speed_kmh: number | null;
    distance_km: number | null;
    detected_trips: number | null;
};
export type RankingPage = {
    data: Ranking[];
    page: Page;
};
export type ApiKey = {
    id: string;
    name: string;
    scopes: ("read:transit" | "read:history")[];
    created_at: string;
    expires_at: string;
    revoked: boolean;
};
export type ApiKeyPage = {
    data: ApiKey[];
    page: Page;
};
export type CreateKey = {
    name: string;
    scopes: ("read:transit" | "read:history")[];
};
export type KeySecret = {
    key: ApiKey;
    secret: string;
};
export type MetroLine = {
    line: string;
    state: string;
    description: string;
};
export type MetroStatus = {
    status: "unconfigured" | "ok" | "error";
    checked_at: string | null;
    message: string;
    source_url: string;
    lines: MetroLine[];
};
/**
 * getHealth
 */
export function getHealth(opts?: Oazapfts.RequestOpts) {
    return oazapfts.ok(oazapfts.fetchJson<{
        status: 200;
        data: Health;
    } | {
        status: number;
        data: Error;
    }>("/api/v1/health", {
        ...opts
    }));
}
/**
 * getConfig
 */
export function getConfig(opts?: Oazapfts.RequestOpts) {
    return oazapfts.ok(oazapfts.fetchJson<{
        status: 200;
        data: Config;
    } | {
        status: number;
        data: Error;
    }>("/api/v1/config", {
        ...opts
    }));
}
/**
 * googleLogin
 */
export function googleLogin(googleLogin: GoogleLogin, opts?: Oazapfts.RequestOpts) {
    return oazapfts.ok(oazapfts.fetchJson<{
        status: 200;
        data: User;
    } | {
        status: number;
        data: Error;
    }>("/api/v1/auth/google", oazapfts.json({
        ...opts,
        method: "POST",
        body: googleLogin
    })));
}
/**
 * developmentLogin
 */
export function developmentLogin(devLogin: DevLogin, opts?: Oazapfts.RequestOpts) {
    return oazapfts.ok(oazapfts.fetchJson<{
        status: 200;
        data: User;
    } | {
        status: number;
        data: Error;
    }>("/api/v1/auth/development", oazapfts.json({
        ...opts,
        method: "POST",
        body: devLogin
    })));
}
/**
 * getMe
 */
export function getMe(opts?: Oazapfts.RequestOpts) {
    return oazapfts.ok(oazapfts.fetchJson<{
        status: 200;
        data: User;
    } | {
        status: number;
        data: Error;
    }>("/api/v1/auth/me", {
        ...opts
    }));
}
/**
 * logout
 */
export function logout(opts?: Oazapfts.RequestOpts) {
    return oazapfts.ok(oazapfts.fetchJson<{
        status: 204;
    } | {
        status: number;
        data: Error;
    }>("/api/v1/auth/logout", {
        ...opts,
        method: "POST"
    }));
}
/**
 * listOperators
 */
export function listOperators({ limit, offset, revision }: {
    limit?: number;
    offset?: number;
    revision?: string;
} = {}, opts?: Oazapfts.RequestOpts) {
    return oazapfts.ok(oazapfts.fetchJson<{
        status: 200;
        data: OperatorPage;
    } | {
        status: number;
        data: Error;
    }>(`/api/v1/operators${QS.query(QS.explode({
        limit,
        offset,
        revision
    }))}`, {
        ...opts
    }));
}
/**
 * listRoutes
 */
export function listRoutes({ limit, offset, revision, operators, q }: {
    limit?: number;
    offset?: number;
    revision?: string;
    operators?: string;
    q?: string;
} = {}, opts?: Oazapfts.RequestOpts) {
    return oazapfts.ok(oazapfts.fetchJson<{
        status: 200;
        data: RoutePage;
    } | {
        status: number;
        data: Error;
    }>(`/api/v1/routes${QS.query(QS.explode({
        limit,
        offset,
        revision,
        operators,
        q
    }))}`, {
        ...opts
    }));
}
/**
 * getRoute
 */
export function getRoute(routeId: string, opts?: Oazapfts.RequestOpts) {
    return oazapfts.ok(oazapfts.fetchJson<{
        status: 200;
        data: RouteDetail;
    } | {
        status: number;
        data: Error;
    }>(`/api/v1/routes/${encodeURIComponent(routeId)}`, {
        ...opts
    }));
}
/**
 * listStops
 */
export function listStops({ limit, offset, revision, operators, routeId, q }: {
    limit?: number;
    offset?: number;
    revision?: string;
    operators?: string;
    routeId?: string;
    q?: string;
} = {}, opts?: Oazapfts.RequestOpts) {
    return oazapfts.ok(oazapfts.fetchJson<{
        status: 200;
        data: StopPage;
    } | {
        status: number;
        data: Error;
    }>(`/api/v1/stops${QS.query(QS.explode({
        limit,
        offset,
        revision,
        operators,
        route_id: routeId,
        q
    }))}`, {
        ...opts
    }));
}
/**
 * listVehicles
 */
export function listVehicles({ limit, offset, revision, operators, routeId }: {
    limit?: number;
    offset?: number;
    revision?: string;
    operators?: string;
    routeId?: string;
} = {}, opts?: Oazapfts.RequestOpts) {
    return oazapfts.ok(oazapfts.fetchJson<{
        status: 200;
        data: VehiclePage;
    } | {
        status: number;
        data: Error;
    }>(`/api/v1/vehicles${QS.query(QS.explode({
        limit,
        offset,
        revision,
        operators,
        route_id: routeId
    }))}`, {
        ...opts
    }));
}
/**
 * listTrips
 */
export function listTrips({ limit, offset, revision, operators, routeId, $from, to }: {
    limit?: number;
    offset?: number;
    revision?: string;
    operators?: string;
    routeId?: string;
    $from?: string;
    to?: string;
} = {}, opts?: Oazapfts.RequestOpts) {
    return oazapfts.ok(oazapfts.fetchJson<{
        status: 200;
        data: TripPage;
    } | {
        status: number;
        data: Error;
    }>(`/api/v1/trips${QS.query(QS.explode({
        limit,
        offset,
        revision,
        operators,
        route_id: routeId,
        "from": $from,
        to
    }))}`, {
        ...opts
    }));
}
/**
 * listArrivals
 */
export function listArrivals({ limit, offset, revision, operators, routeId, $from, to, stopId }: {
    limit?: number;
    offset?: number;
    revision?: string;
    operators?: string;
    routeId?: string;
    $from?: string;
    to?: string;
    stopId?: string;
} = {}, opts?: Oazapfts.RequestOpts) {
    return oazapfts.ok(oazapfts.fetchJson<{
        status: 200;
        data: ArrivalPage;
    } | {
        status: number;
        data: Error;
    }>(`/api/v1/arrivals${QS.query(QS.explode({
        limit,
        offset,
        revision,
        operators,
        route_id: routeId,
        "from": $from,
        to,
        stop_id: stopId
    }))}`, {
        ...opts
    }));
}
/**
 * getMetrics
 */
export function getMetrics({ operators, routeId, $from, to, revision }: {
    operators?: string;
    routeId?: string;
    $from?: string;
    to?: string;
    revision?: string;
} = {}, opts?: Oazapfts.RequestOpts) {
    return oazapfts.ok(oazapfts.fetchJson<{
        status: 200;
        data: Metrics;
    } | {
        status: number;
        data: Error;
    }>(`/api/v1/metrics${QS.query(QS.explode({
        operators,
        route_id: routeId,
        "from": $from,
        to,
        revision
    }))}`, {
        ...opts
    }));
}
/**
 * listHistory
 */
export function listHistory({ limit, offset, revision, operators, routeId, $from, to }: {
    limit?: number;
    offset?: number;
    revision?: string;
    operators?: string;
    routeId?: string;
    $from?: string;
    to?: string;
} = {}, opts?: Oazapfts.RequestOpts) {
    return oazapfts.ok(oazapfts.fetchJson<{
        status: 200;
        data: HistoryPointPage;
    } | {
        status: number;
        data: Error;
    }>(`/api/v1/history${QS.query(QS.explode({
        limit,
        offset,
        revision,
        operators,
        route_id: routeId,
        "from": $from,
        to
    }))}`, {
        ...opts
    }));
}
/**
 * listFleet
 */
export function listFleet({ limit, offset, revision, operators, routeId, $from, to, q, sort }: {
    limit?: number;
    offset?: number;
    revision?: string;
    operators?: string;
    routeId?: string;
    $from?: string;
    to?: string;
    q?: string;
    sort?: "speed" | "distance" | "trips" | "last_seen" | "first_seen" | "vehicle" | "model";
} = {}, opts?: Oazapfts.RequestOpts) {
    return oazapfts.ok(oazapfts.fetchJson<{
        status: 200;
        data: FleetVehiclePage;
    } | {
        status: number;
        data: Error;
    }>(`/api/v1/fleet${QS.query(QS.explode({
        limit,
        offset,
        revision,
        operators,
        route_id: routeId,
        "from": $from,
        to,
        q,
        sort
    }))}`, {
        ...opts
    }));
}
/**
 * listTraffic
 */
export function listTraffic({ limit, offset, revision, operators, routeId, $from, to, hourStart, hourEnd, weekdaysOnly }: {
    limit?: number;
    offset?: number;
    revision?: string;
    operators?: string;
    routeId?: string;
    $from?: string;
    to?: string;
    hourStart?: number;
    hourEnd?: number;
    weekdaysOnly?: boolean;
} = {}, opts?: Oazapfts.RequestOpts) {
    return oazapfts.ok(oazapfts.fetchJson<{
        status: 200;
        data: TrafficPointPage;
    } | {
        status: number;
        data: Error;
    }>(`/api/v1/traffic${QS.query(QS.explode({
        limit,
        offset,
        revision,
        operators,
        route_id: routeId,
        "from": $from,
        to,
        hour_start: hourStart,
        hour_end: hourEnd,
        weekdays_only: weekdaysOnly
    }))}`, {
        ...opts
    }));
}
/**
 * listRankings
 */
export function listRankings({ limit, offset, revision, operators, routeId, $from, to, sort }: {
    limit?: number;
    offset?: number;
    revision?: string;
    operators?: string;
    routeId?: string;
    $from?: string;
    to?: string;
    sort?: "speed" | "distance" | "trips" | "last_seen" | "first_seen" | "vehicle" | "model";
} = {}, opts?: Oazapfts.RequestOpts) {
    return oazapfts.ok(oazapfts.fetchJson<{
        status: 200;
        data: RankingPage;
    } | {
        status: number;
        data: Error;
    }>(`/api/v1/rankings${QS.query(QS.explode({
        limit,
        offset,
        revision,
        operators,
        route_id: routeId,
        "from": $from,
        to,
        sort
    }))}`, {
        ...opts
    }));
}
/**
 * listKeys
 */
export function listKeys({ limit, offset }: {
    limit?: number;
    offset?: number;
} = {}, opts?: Oazapfts.RequestOpts) {
    return oazapfts.ok(oazapfts.fetchJson<{
        status: 200;
        data: ApiKeyPage;
    } | {
        status: number;
        data: Error;
    }>(`/api/v1/keys${QS.query(QS.explode({
        limit,
        offset
    }))}`, {
        ...opts
    }));
}
/**
 * createKey
 */
export function createKey(createKey: CreateKey, opts?: Oazapfts.RequestOpts) {
    return oazapfts.ok(oazapfts.fetchJson<{
        status: 201;
        data: KeySecret;
    } | {
        status: number;
        data: Error;
    }>("/api/v1/keys", oazapfts.json({
        ...opts,
        method: "POST",
        body: createKey
    })));
}
/**
 * revokeKey
 */
export function revokeKey(keyId: string, opts?: Oazapfts.RequestOpts) {
    return oazapfts.ok(oazapfts.fetchJson<{
        status: 204;
    } | {
        status: number;
        data: Error;
    }>(`/api/v1/keys/${encodeURIComponent(keyId)}`, {
        ...opts,
        method: "DELETE"
    }));
}
/**
 * Verified direct Metro service status (server-side consumer credentials)
 */
export function getMetroStatus(opts?: Oazapfts.RequestOpts) {
    return oazapfts.ok(oazapfts.fetchJson<{
        status: 200;
        data: MetroStatus;
    } | {
        status: number;
        data: Error;
    }>("/api/v1/metro/status", {
        ...opts
    }));
}
