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
    history_resolution_seconds: number;
    history_storage_limit_bytes: number | null;
    history_collection_status: "collecting" | "paused" | "unavailable";
    /** Local live collection cadence, not a source freshness guarantee. */
    live_refresh_seconds: number;
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
    last_known_positions: number;
    /** Legacy cache truncation indicator; new publications retain all admitted identities until their ten-minute source-clock deadline. */
    last_known_truncated: boolean;
};
export type Page = {
    limit: number;
    offset: number;
    total: number;
    has_more: boolean;
    /** Endpoint-specific immutable view token. Vehicle v: tokens pin first-page eligibility time for at most five minutes, subject to version eviction; clients must remove positions whose original expiry has passed. CP predictions use schedule tokens and reject the whole pinned result with410 when its earliest included original source expiry passes. Arrivals with provider rows use bounded a: result tokens which freeze selectors, interval, rows and availability. Expiry, eviction or static generation changes return410. Planned-only GTFS pages preserve the existing t: network revision behavior. */
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
export type ScheduledEndpoints = {
    origin_source_stop_id: string;
    origin_name: string;
    destination_source_stop_id: string;
    destination_name: string;
    source_url: string;
    /** Explicit validated GTFS service date, YYYY-MM-DD. */
    service_date: string;
};
export type VehicleReference = {
    vehicle_id: string;
    reference: string;
};
export type ReportingState = {
    state: "reporting" | "not_reporting" | "unknown";
    reason: "current" | "missing_from_snapshot" | "observation_old" | "source_error" | "source_unverified" | "collection_old";
    /** Application clock when the state or reason last changed. */
    state_changed_at: string;
    /** Latest accepted original provider observation clock; repeated membership does not advance it. */
    last_observed_at: string | null;
    /** Application collection clock of the latest successful normalized snapshot containing this identity. */
    last_seen_at: string | null;
    /** Whether this exact latest state and clocks have committed to durable storage; false while a write is pending or failed. */
    persisted: boolean;
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
    /** Published source code or string; code schemes differ by provider/feed version. Metadata recorded with the observation, not a depot assignment. */
    typology: string | null;
    /** Published source code or string; code schemes differ by provider/feed version. Metadata recorded with the observation, not a depot assignment. */
    propulsion: string | null;
    /** Previous position, excluded from current metrics. */
    last_known: boolean;
    /** Legacy observation-plus-five-minutes timestamp. Clients derive the five-minute no-update warning from observed_at for every operator; this field does not control membership or metrics and does not establish physical inactivity. */
    inactive_at: string;
    /** Position display deadline, ten minutes after original observation, including current rows so disconnected clients can expire cached data. Clients must remove expired positions even from pinned pages. */
    last_known_expires_at: string | null;
    /** Published progress relative to a stop, not measured movement; estimated positions imply estimated status. */
    current_status?: ("STOPPED_AT" | "INCOMING_AT" | "IN_TRANSIT_TO") | null;
    /** Original stop reference; does not establish the current vehicle location. */
    source_stop_id?: string | null;
    /** Exact verified retained application stop reference. */
    stop_id?: string | null;
    stop_name?: string | null;
    /** Validated explicit source service date, YYYY-MM-DD; never inferred from collection time. */
    operational_date?: string | null;
    scheduled_service?: ScheduledEndpoints;
    /** Optional published vehicle specification, not live free places or occupancy. Exact source identity only. */
    seated_capacity?: number | null;
    /** Optional published vehicle specification, not live free places or occupancy. Exact source identity only. */
    total_capacity?: number | null;
    /** Optional published equipment value. Some upstream schemas default false when unspecified; false does not prove equipment absence or working availability. */
    wheelchair_accessible?: boolean | null;
    /** Optional published equipment value. Some upstream schemas default false when unspecified; false does not prove equipment absence or working availability. */
    contactless?: boolean | null;
    /** Published commercial service number, never inferred from an internal ID. */
    service_label?: string;
    vehicle_ref?: VehicleReference;
    /** Provider-published pattern identity, qualified by operator and original plan/agency. Does not establish operating day or schedule. */
    pattern_id?: string;
    reporting?: ReportingState;
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
    /** Scheduled departure at the first retained stop in the Lisbon area; not necessarily the full journey origin. */
    planned_departure: string;
    /** Scheduled arrival at the last retained stop in the Lisbon area; not necessarily the full journey destination. */
    planned_end: string;
    kind: "scheduled";
    scheduled_service?: ScheduledEndpoints;
    /** Published commercial service number, never inferred from an internal ID. */
    service_label?: string;
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
    plan_id?: string | null;
    source_trip_id?: string | null;
    service_date?: string | null;
    stop_sequence?: number | null;
    route_name?: string | null;
    /** Published commercial service number, never inferred from an internal ID. */
    service_label?: string;
    vehicle_ref?: VehicleReference;
    /** Original underlying prediction observation time, when published. Collection does not renew it. */
    source_updated_at?: string;
    /** Display expiry. TML predictions expire from their original observation; CM uses bounded collection lifetime because its source update clock is not published. */
    valid_until?: string;
    /** Present only for a unique verified vehicle and compatible published operational date. No association is inferred from proximity. */
    vehicle_id?: string;
    /** Basis of a prediction service date. matched_schedule is uniquely inferred from GTFS and events; it is not an observed vehicle operational date. */
    date_basis?: "published" | "matched_schedule";
};
export type ArrivalAvailability = {
    status: "loading" | "ok" | "partial" | "stale" | "error" | "unavailable";
    planned_status: "ok" | "partial" | "unavailable";
    message: string;
    source_url: string;
    collected_at?: string;
    /** Most recent original observation among relevant published predictions, or an original stale observation. Absent when the source does not publish this clock. */
    source_updated_at?: string;
    valid_until?: string;
    coverage_until?: string;
};
export type ArrivalPage = {
    data: Arrival[];
    page: Page;
    availability?: ArrivalAvailability;
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
    /** Coverage of current totals, independently of historical observations. */
    live_coverage: "complete" | "partial" | "unavailable";
    unavailable_live_operators: string[];
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
    /** Published source code or string; code schemes differ by provider/feed version. Metadata recorded with the observation, not a depot assignment. */
    typology: string | null;
    /** Published source code or string; code schemes differ by provider/feed version. Metadata recorded with the observation, not a depot assignment. */
    propulsion: string | null;
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
export type RouteShape = {
    id: string;
    operator_id: string;
    route_id: string;
    shape_id: string;
    direction_id: number | null;
    headsign: string;
    color: string;
    plan_id: string;
    source_url: string;
    updated_at: string;
    geometry: number[][];
};
export type GeometryCoverage = {
    operator_id: string;
    status: "available" | "stale" | "unavailable" | "partial";
    updated_at: string | null;
    message: string;
};
export type RouteShapePage = {
    data: RouteShape[];
    page: Page;
    coverage: GeometryCoverage[];
};
export type OperatorCoverage = {
    operator_id: string;
    vehicles: number;
    reported_vehicles: number;
    estimated_vehicles: number;
    speed_samples: number;
    model_vehicles: number;
    plate_vehicles: number;
    typology_vehicles: number;
};
export type OperatorCoveragePage = {
    data: OperatorCoverage[];
    page: Page;
};
export type CpPrediction = {
    id: string;
    operator_id: string;
    plan_id: string;
    source_trip_id: string;
    stop_id: string;
    route_id: string;
    stop_name: string;
    route_name: string;
    destination_name: string;
    service_label: string | null;
    service_date: string | null;
    date_basis: ("published" | "matched_schedule") | null;
    stop_sequence: number;
    scheduled_at: string | null;
    expected_at: string;
    delay_seconds: number | null;
    source_updated_at: string;
    collected_at: string;
    valid_until: string;
    source_url: string;
    vehicle_ref?: VehicleReference;
    expected_departure_at?: string | null;
    scheduled_departure_at?: string | null;
};
export type CpPredictionAvailability = {
    status: "loading" | "ok" | "partial" | "stale" | "error";
    message: string;
    collected_at: string | null;
    published_at: string | null;
    excluded_updates: number;
    source_url: string;
};
export type CpPredictionPage = {
    data: CpPrediction[];
    page: Page;
    availability: CpPredictionAvailability;
};
export type VehicleCall = {
    id: string;
    stop_id: string;
    stop_name: string;
    stop?: Stop;
    stop_sequence?: number;
    kind: "predicted" | "scheduled" | "published_route";
    scheduled_at?: string;
    expected_at?: string;
    source_updated_at?: string;
    delay_seconds?: number;
    source_url: string;
    /** Published static plan of this stop, response-only navigation provenance. */
    stop_plan_id?: string;
    /** Static network revision timestamp; revalidate the target catalog before opening a station. */
    stop_static_updated_at?: string;
};
export type VehicleCallsPage = {
    vehicle: Vehicle;
    data: VehicleCall[];
    page: Page;
    availability: "available" | "partial" | "plan_mismatch" | "unidentified_service" | "next_stop_only" | "unavailable";
    coverage: "regional_subset" | "complete_published_route";
    progress: "known" | "unknown";
    /** Earliest expiry of the whole pinned CP prediction result, including rows on later pages. */
    valid_until?: string;
    /** Exact published pattern geometry from the frozen revision. Only present on page zero when include_geometry=true; does not change vehicle position. */
    geometry?: RouteShape;
};
export type BoardDirection = {
    line_key: string;
    line_name: string;
    color: string;
    direction_key: string | null;
    label: string;
    /** Number of assembled call results for this direction in the board window; null when overall board evidence is unavailable, stale or loading. Not a count of distinct vehicles or proven operated journeys. */
    count: number | null;
};
export type PopupCoverage = {
    status: "available" | "partial" | "unavailable" | "stale" | "loading";
    message: string;
    actual_arrivals: boolean;
    actual_departures: boolean;
    history_collection_status: "collecting" | "paused" | "unavailable";
    /** For station board/call responses, the latest original source update among currently usable predictions in the returned results; null when no usable prediction supplies this clock. Collection or rendering does not renew it. */
    source_updated_at: string | null;
};
export type StopBoard = {
    directions: BoardDirection[];
    coverage: PopupCoverage;
    revision: string;
};
export type CallTimeEvidence = {
    at: string;
    source_url: string;
    source_updated_at: string | null;
    collected_at: string | null;
    valid_until: string | null;
    delay_seconds?: number | null;
    /** Present for experimental own forecasts; not a source observation clock. */
    model_version?: string;
    /** Supporting experimental episode, distinct from popup journey and transport cursor. */
    association_episode?: string;
};
export type MetroEventEvidence = {
    at: string;
    window_start: string;
    window_end: string;
    mode: "inferred_arrival" | "model_departure";
    source_url: string;
    model_version: string;
    persistence: "pending" | "committed" | "unavailable";
    reason: string;
};
export type CallTime = {
    kind: "actual" | "prediction" | "schedule" | "unavailable" | "inferred";
    at: string | null;
    reason: string;
    actual: (CallTimeEvidence) | null;
    prediction: (CallTimeEvidence) | null;
    schedule: (CallTimeEvidence) | null;
    inferred?: (MetroEventEvidence) | null;
};
export type MetroDepartureRevision = {
    revision: number;
    status: "estimated" | "withdrawn";
    source_at: string;
    reason: string;
    evidence?: MetroEventEvidence;
};
export type MetroPlatformForecast = {
    platform: string;
    source_updated_at: string;
    at: string | null;
    valid_until: string;
};
export type MetroForecastAssociation = {
    source_reference: string | null;
    estimated_reference: string | null;
    method: "published" | "unknown" | "order";
    evidence_at: string | null;
    anchors: string[];
    platforms: MetroPlatformForecast[];
    limitations: string[];
};
export type StopCall = {
    id: string;
    journey_id: string | null;
    stop_id: string;
    stop_name: string;
    stop_sequence: number;
    line_key: string;
    direction_key: string | null;
    destination: string;
    arrival: CallTime;
    departure: CallTime;
    phase: "previous" | "current" | "future" | "unknown";
    service_label?: string | null;
    stop?: Stop;
    stop_static_updated_at?: string;
    /** Static plan that supplied the stop, used with stop_static_updated_at to revalidate navigation. Absent when the static source has no plan identity. */
    stop_plan_id?: string;
    vehicle_ref?: VehicleReference;
    own_prediction?: (CallTimeEvidence) | null;
    departure_revisions?: MetroDepartureRevision[];
    metro_forecast?: MetroForecastAssociation;
};
export type StopCallPage = {
    data: StopCall[];
    page: Page;
    coverage: PopupCoverage;
};
export type VehicleJourney = {
    association: "resolved" | "published_route" | "unresolved" | "ambiguous";
    message: string;
    journey_id: string | null;
    line_name: string;
    direction: string;
    destination: string;
    progress: "confirmed" | "estimated" | "unknown";
    next_index: number | null;
    complete: boolean;
    data: StopCall[];
    page: Page;
    coverage: PopupCoverage;
};
export type MetroHourPattern = {
    profile: string;
    offset_seconds: number;
    /** Common retained histogram resolution in seconds; zero means incompatible widths have no supported common resolution. */
    resolution_seconds: number;
    condition: string;
    hour: number;
    direction: string;
    route: string;
    platform: string;
    day_type: string;
    signals: number;
    days: number;
    probability: number | null;
    mean_headway_seconds: number | null;
    mean_component_seconds: number | null;
    component_target: string;
    component_samples: number;
};
export type MetroPatternComponent = {
    origin: string;
    target: string;
    origin_at: string;
    seconds: number;
    samples: number;
    days: number;
    historical_fallback: boolean;
    general_context: boolean;
    oldest_date: string;
    newest_date: string;
};
export type MetroPatternForecast = {
    id: string;
    episode: string;
    issued_at: string;
    route: string;
    direction: string;
    stop: string;
    stop_name: string;
    destination_name: string;
    /** Published Metro platform value, or visit:<stop_sequence> for later-stage adapters. A published visit is not a physical platform assignment. */
    platform: string;
    /** Published entity identity within its operator context; an unassociated official prediction may use its published trip identity. This does not establish a physical fleet unit. */
    train: string;
    "function": string;
    mode: string;
    profile: string;
    condition: string;
    /** Original source clock associated with the point, never renewed by collection. Null when an official publication omits its clock; collection-bounded validity remains private. */
    source_at: string | null;
    official_at: string | null;
    own_at: string | null;
    lower_at: string | null;
    upper_at: string | null;
    unavailable: string;
    components: MetroPatternComponent[];
    calibration_samples: number;
    selected: boolean;
    evaluated: boolean;
    reference_lower: string | null;
    reference_upper: string | null;
    error_lower: number | null;
    error_upper: number | null;
    result: string;
};
export type MetroEvaluationReport = {
    cohort: string;
    support: string;
    "function": string;
    direction: string;
    route: string;
    mode: string;
    profile: string;
    condition: string;
    horizon: number;
    cases: number;
    official_available: number;
    own_available: number;
    paired: number;
    evaluated: number;
    journeys: number;
    days: number;
    mae_own_lower: number | null;
    mae_own_upper: number | null;
    mae_official_lower: number | null;
    mae_official_upper: number | null;
    p90_own_lower: number | null;
    p90_own_upper: number | null;
    p90_official_lower: number | null;
    p90_official_upper: number | null;
    band_cases: number;
    band_certain: number;
    band_possible: number;
    journey_count_complete: boolean;
};
export type TransportOperatorHistory = {
    operator: string;
    enabled: boolean;
    status: string;
    forecasts: boolean;
    physical_validation: boolean;
    as_of: string | null;
    samples: number;
};
export type MetroPatterns = {
    status: string;
    message: string;
    experimental: boolean;
    physical_validation: boolean;
    as_of: string | null;
    profile: string;
    storage_bytes: number;
    limit_bytes: number;
    sample_seconds: number;
    bin_seconds: number;
    detail_days: number;
    aggregate_months: number;
    collected_days: number;
    gaps: number;
    pending: number;
    evaluated: number;
    lost_reference: number;
    dwell_seconds: number | null;
    speed_kmh: number | null;
    patterns: MetroHourPattern[];
    forecasts: MetroPatternForecast[];
    evaluation: MetroEvaluationReport[];
    current_day_type: string;
    calendar: string;
    operators: TransportOperatorHistory[];
    operator: string;
};
export type MetroJourneyPersistence = {
    state: "pending" | "committed" | "unavailable";
    revision: number;
    committed_revision: number;
    generation: string | null;
    committed_at: string | null;
};
export type MetroJourneyLifecycle = {
    state: "active" | "completed" | "superseded";
    reason: string;
    at?: string | null;
    first_movement_at?: string | null;
    direction_confirmed_at?: string | null;
    successor_journey_id?: string | null;
    predecessor_journey_id?: string | null;
};
export type MetroDirectionEvidence = {
    state: "unknown" | "context" | "confirmed";
    reason: string;
    confirmed_at?: string | null;
    first_movement_at?: string | null;
    geometry_version?: string | null;
};
export type MetroModelProjection = {
    model_version: string;
    geometry_version: string;
    source_updated_at: string;
    valid_until: string;
    from_at: string;
    to_at: string;
    from_lat: number;
    from_lon: number;
    to_lat: number;
    to_lon: number;
};
export type MetroTrain = {
    journey_id: string;
    reference: string;
    route_id: string;
    direction_code: string;
    destination: string;
    association: "supported" | "suspended";
    reason: string;
    source_updated_at: string;
    valid_until: string;
    next_index: number | null;
    calls: StopCall[];
    vehicle_id: string | null;
    /** Supported current inferred station visit; null if no admissible stopped evidence. */
    current_index?: number | null;
    persistence?: MetroJourneyPersistence;
    lifecycle?: MetroJourneyLifecycle;
    direction_evidence?: MetroDirectionEvidence;
    model_projection?: MetroModelProjection;
    /** Whether compatible published paths agree on the origin; false means shared geometry only, not a known full journey. */
    origin_known?: boolean;
};
export type MetroJourneyRecovery = {
    status: "none" | "current" | "historical" | "partial" | "recovering" | "expired" | "unavailable" | "corrupt";
    requested_journey_id: string | null;
    reason: string;
};
export type MetroForecastContext = {
    reference: string;
    route_id: string;
    direction_code: string | null;
    destination: string;
    status: "admissible" | "incompatible";
    reason: string;
    calls: StopCall[];
    /** Whether all compatible source paths agree on the origin. A shared downstream axis cannot fabricate earlier visits. */
    origin_known?: boolean;
};
export type MetroLiveFrame = {
    revision: string;
    published_at: string;
    plan_id: string;
    status: MetroStatus;
    vehicles: Vehicle[];
    trains: MetroTrain[];
    directions: BoardDirection[];
    selected_journey_id: string | null;
    history_status: string;
    /** Usable official or independently supported own predictions without a qualified current journey association; never a fabricated map link. */
    unassociated_forecasts: StopCall[];
    recovery?: MetroJourneyRecovery;
    forecast_contexts?: MetroForecastContext[];
    association_reason?: string;
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
export function listVehicles({ limit, offset, revision, operators, routeId, stopId }: {
    limit?: number;
    offset?: number;
    revision?: string;
    operators?: string;
    routeId?: string;
    stopId?: string;
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
        route_id: routeId,
        stop_id: stopId
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
/**
 * Official route geometry variants and coverage
 */
export function listRouteShapes({ limit, offset, revision, operators, routeId }: {
    limit?: number;
    offset?: number;
    revision?: string;
    operators?: string;
    routeId?: string;
} = {}, opts?: Oazapfts.RequestOpts) {
    return oazapfts.ok(oazapfts.fetchJson<{
        status: 200;
        data: RouteShapePage;
    } | {
        status: number;
        data: Error;
    }>(`/api/v1/route-shapes${QS.query(QS.explode({
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
 * Committed per-operator historical coverage
 */
export function listOperatorCoverage({ limit, offset, revision, operators, routeId, $from, to, hourStart, hourEnd, weekdaysOnly }: {
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
        data: OperatorCoveragePage;
    } | {
        status: number;
        data: Error;
    }>(`/api/v1/operator-coverage${QS.query(QS.explode({
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
 * Public CP stop-call predictions with original clocks and partial coverage
 */
export function listCpPredictions({ limit, offset, revision, operators, routeId, $from, to, stopId, tripId }: {
    limit?: number;
    offset?: number;
    revision?: string;
    operators?: string;
    routeId?: string;
    $from?: string;
    to?: string;
    stopId?: string;
    tripId?: string;
} = {}, opts?: Oazapfts.RequestOpts) {
    return oazapfts.ok(oazapfts.fetchJson<{
        status: 200;
        data: CpPredictionPage;
    } | {
        status: number;
        data: Error;
    }>(`/api/v1/cp/predictions${QS.query(QS.explode({
        limit,
        offset,
        revision,
        operators,
        route_id: routeId,
        "from": $from,
        to,
        stop_id: stopId,
        trip_id: tripId
    }))}`, {
        ...opts
    }));
}
/**
 * Read cached visits of a frozen vehicle/service
 */
export function getVehicleCalls(vehicleId: string, { reference, limit, offset, revision, includeGeometry }: {
    reference?: string;
    limit?: number;
    offset?: number;
    revision?: string;
    includeGeometry?: boolean;
} = {}, opts?: Oazapfts.RequestOpts) {
    return oazapfts.ok(oazapfts.fetchJson<{
        status: 200;
        data: VehicleCallsPage;
    } | {
        status: number;
        data: Error;
    }>(`/api/v1/vehicles/${encodeURIComponent(vehicleId)}/calls${QS.query(QS.explode({
        reference,
        limit,
        offset,
        revision,
        include_geometry: includeGeometry
    }))}`, {
        ...opts
    }));
}
/**
 * Cached directions and independent arrival/departure times
 */
export function getStopBoard(stopId: string, { revision, $from, to }: {
    revision?: string;
    $from?: string;
    to?: string;
} = {}, opts?: Oazapfts.RequestOpts) {
    return oazapfts.ok(oazapfts.fetchJson<{
        status: 200;
        data: StopBoard;
    } | {
        status: number;
        data: Error;
    }>(`/api/v1/stops/${encodeURIComponent(stopId)}/board${QS.query(QS.explode({
        revision,
        "from": $from,
        to
    }))}`, {
        ...opts
    }));
}
/**
 * Cached directions and independent arrival/departure times
 */
export function listStopCalls(stopId: string, { lineKey, directionKey, limit, offset, revision, $from, to }: {
    lineKey?: string;
    directionKey?: string;
    limit?: number;
    offset?: number;
    revision?: string;
    $from?: string;
    to?: string;
} = {}, opts?: Oazapfts.RequestOpts) {
    return oazapfts.ok(oazapfts.fetchJson<{
        status: 200;
        data: StopCallPage;
    } | {
        status: number;
        data: Error;
    }>(`/api/v1/stops/${encodeURIComponent(stopId)}/board/calls${QS.query(QS.explode({
        line_key: lineKey,
        direction_key: directionKey,
        limit,
        offset,
        revision,
        "from": $from,
        to
    }))}`, {
        ...opts
    }));
}
/**
 * Cached directions and independent arrival/departure times
 */
export function getVehicleJourney(vehicleId: string, { limit, offset, revision }: {
    limit?: number;
    offset?: number;
    revision?: string;
} = {}, opts?: Oazapfts.RequestOpts) {
    return oazapfts.ok(oazapfts.fetchJson<{
        status: 200;
        data: VehicleJourney;
    } | {
        status: number;
        data: Error;
    }>(`/api/v1/vehicles/${encodeURIComponent(vehicleId)}/journey${QS.query(QS.explode({
        limit,
        offset,
        revision
    }))}`, {
        ...opts
    }));
}
/**
 * Experimental Metro patterns and official/own forecasts; no physical validation
 */
export function getMetroPatterns({ stopId, episode }: {
    stopId?: string;
    episode?: string;
} = {}, opts?: Oazapfts.RequestOpts) {
    return oazapfts.ok(oazapfts.fetchJson<{
        status: 200;
        data: MetroPatterns;
    } | {
        status: number;
        data: Error;
    }>(`/api/v1/metro/patterns${QS.query(QS.explode({
        stop_id: stopId,
        episode
    }))}`, {
        ...opts
    }));
}
/**
 * Experimental transport patterns and separate official/own forecasts
 */
export function getTransportPatterns(operatorId: "metro" | "cm" | "carris" | "cp" | "fertagus" | "ttsl" | "tcb" | "mobi", { stopId, episode }: {
    stopId?: string;
    episode?: string;
} = {}, opts?: Oazapfts.RequestOpts) {
    return oazapfts.ok(oazapfts.fetchJson<{
        status: 200;
        data: MetroPatterns;
    } | {
        status: number;
        data: Error;
    }>(`/api/v1/transport/patterns${QS.query(QS.explode({
        operator_id: operatorId,
        stop_id: stopId,
        episode
    }))}`, {
        ...opts
    }));
}
/**
 * Coherent Metro map and selected popup updates
 */
export function getMetroLive({ routeId, vehicleId, journeyId, stopId, ifNoneMatch }: {
    routeId?: string;
    vehicleId?: string;
    journeyId?: string;
    stopId?: string;
    ifNoneMatch?: string;
} = {}, opts?: Oazapfts.RequestOpts) {
    return oazapfts.ok(oazapfts.fetchJson<{
        status: 200;
        data: MetroLiveFrame;
    } | {
        status: 304;
    } | {
        status: number;
        data: Error;
    }>(`/api/v1/metro/live${QS.query(QS.explode({
        route_id: routeId,
        vehicle_id: vehicleId,
        journey_id: journeyId,
        stop_id: stopId
    }))}`, {
        ...opts,
        headers: oazapfts.mergeHeaders(opts?.headers, {
            "If-None-Match": ifNoneMatch
        })
    }));
}
/**
 * Coherent Metro map and selected popup updates
 */
export function streamMetroLive({ routeId, vehicleId, journeyId, stopId }: {
    routeId?: string;
    vehicleId?: string;
    journeyId?: string;
    stopId?: string;
} = {}, opts?: Oazapfts.RequestOpts) {
    return oazapfts.ok(oazapfts.fetchJson<{
        status: 200;
        data: string;
    } | {
        status: number;
        data: Error;
    }>(`/api/v1/metro/live/stream${QS.query(QS.explode({
        route_id: routeId,
        vehicle_id: vehicleId,
        journey_id: journeyId,
        stop_id: stopId
    }))}`, {
        ...opts
    }));
}
