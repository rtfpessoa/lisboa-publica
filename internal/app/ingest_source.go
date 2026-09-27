package app

type hubPosition struct {
	Status          *string  `json:"current_status"`
	Stop            *string  `json:"stop_id"`
	OperationalDate int      `json:"operational_date"`
	Agency          string   `json:"agency_id"`
	ID              string   `json:"vehicle_id"`
	Route           string   `json:"route_id"`
	RouteName       string   `json:"route_short_name"`
	Trip            string   `json:"trip_id"`
	Lat             float64  `json:"latitude"`
	Lon             float64  `json:"longitude"`
	At              int64    `json:"created_at"`
	Bearing         *float64 `json:"bearing"`
	Plate           *string  `json:"license_plate"`
}
type hubPlan struct {
	ID     string `json:"_id"`
	Agency string `json:"agency_id"`
	Active bool   `json:"is_active"`
	From   int    `json:"active_from"`
	Until  int    `json:"active_until"`
	URL    string `json:"operation_gtfs_normalized_url"`
}
