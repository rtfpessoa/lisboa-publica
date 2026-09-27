package app

import (
	"lisboapublica/internal/api"
	"strconv"
	"strings"
)

func publishedModel(makeName, model string) string {
	makeName, model = strings.TrimSpace(makeName), strings.TrimSpace(model)
	fields := strings.Fields(model)
	if len(fields)%2 == 0 && len(fields) > 0 {
		half := len(fields) / 2
		if strings.EqualFold(strings.Join(fields[:half], " "), strings.Join(fields[half:], " ")) {
			model = strings.Join(fields[:half], " ")
		}
	}
	if model == "" {
		return makeName
	}
	if makeName == "" || strings.HasPrefix(strings.ToLower(model), strings.ToLower(makeName)) {
		return model
	}
	return makeName + " " + model
}

func metadataRow(row map[string]string) Metadata {
	return Metadata{Model: publishedModel(row["make"], row["model"]), Plate: strings.TrimSpace(row["license_plate"]), Typology: strings.TrimSpace(row["typology"]), Propulsion: strings.TrimSpace(row["propulsion"]), TotalCapacity: capacityText(row["total_capacity"]), WheelchairAccessible: wheelchairEnum(row["wheelchair_accessible"])}
}

func mergeMetadata(previous, next Metadata) Metadata {
	if next.Model != "" {
		previous.Model = next.Model
	}
	if next.Plate != "" {
		previous.Plate = next.Plate
	}
	if next.Typology != "" {
		previous.Typology = next.Typology
	}
	if next.Propulsion != "" {
		previous.Propulsion = next.Propulsion
	}
	if next.SeatedCapacity != nil {
		previous.SeatedCapacity = next.SeatedCapacity
	}
	if next.TotalCapacity != nil {
		previous.TotalCapacity = next.TotalCapacity
	}
	if next.WheelchairAccessible != nil {
		previous.WheelchairAccessible = next.WheelchairAccessible
	}
	if next.Contactless != nil {
		previous.Contactless = next.Contactless
	}
	return previous
}

type publishedMetadata struct {
	ID          string `json:"vehicle_id"`
	Agency      string `json:"agency_id"`
	Make        string `json:"make"`
	Model       string `json:"model"`
	Plate       string `json:"license_plate"`
	Propulsion  string `json:"propulsion"`
	Seats       *int   `json:"available_seats"`
	Wheelchair  *bool  `json:"wheelchair"`
	Contactless *bool  `json:"contactless"`
}

func mergePublishedMetadata(d *StaticData, id string, rows []publishedMetadata) *StaticData {
	codes := map[string]string{"LA77N": "41", "BNA17": "42", "YA15B": "43", "A2L1N": "44", "HF16N": "21"}
	copyData := *d
	copyData.Models = map[string]Metadata{}
	for k, v := range d.Models {
		copyData.Models[k] = v
	}
	for _, row := range rows {
		code, known := codes[row.Agency]
		if !known || !strings.HasPrefix(row.ID, code+"-") {
			continue
		}
		if (id == "mobi") != (row.Agency == "HF16N") {
			continue
		}
		key := strings.TrimPrefix(row.ID, code+"-")
		if id == "cm" {
			key = "[" + row.Agency + "]" + key
		}
		copyData.Models[key] = mergeMetadata(copyData.Models[key], Metadata{Model: publishedModel(row.Make, row.Model), Plate: row.Plate, Propulsion: boundedPropulsion(row.Propulsion), SeatedCapacity: publishedCapacity(row.Seats), WheelchairAccessible: row.Wheelchair, Contactless: row.Contactless})
	}
	return &copyData
}

const maxPublishedCapacity = 10000
const maxPropulsionBytes = 128

func publishedCapacity(value *int) *int {
	if value == nil || *value < 0 || *value > maxPublishedCapacity {
		return nil
	}
	return value
}
func capacityText(value string) *int {
	n, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return nil
	}
	return publishedCapacity(&n)
}
func wheelchairEnum(value string) *bool {
	switch strings.TrimSpace(value) {
	case "1":
		return ptr(true)
	case "2":
		return ptr(false)
	}
	return nil
}
func boundedPropulsion(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > maxPropulsionBytes {
		return ""
	}
	return value
}

func enrichSpecifications(v *api.Vehicle, m Metadata) {
	if v.SeatedCapacity == nil {
		v.SeatedCapacity = m.SeatedCapacity
	}
	if v.TotalCapacity == nil {
		v.TotalCapacity = m.TotalCapacity
	}
	if v.WheelchairAccessible == nil {
		v.WheelchairAccessible = m.WheelchairAccessible
	}
	if v.Contactless == nil {
		v.Contactless = m.Contactless
	}
}
