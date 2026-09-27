package app

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func readFilterSelectors(filter *Filter, query url.Values) error {
	if v := query.Get("operators"); v != "" {
		for _, id := range strings.Split(v, ",") {
			if _, ok := providerByID(id); !ok {
				return fail(http.StatusBadRequest, "operator", "Operador desconhecido.")
			}
			filter.Operators = append(filter.Operators, id)
		}
	}
	if filter.Route != "" {
		parts := strings.SplitN(filter.Route, ":", 2)
		if len(parts) != 2 {
			return fail(http.StatusBadRequest, "route", "ID de carreira inválido.")
		}
		if _, ok := providerByID(parts[0]); !ok {
			return fail(http.StatusBadRequest, "route", "Operador da carreira desconhecido.")
		}
	}
	return nil
}

func readFilterDates(filter *Filter, query url.Values, historical bool) error {
	if err := restoreFilterDates(filter, historical); err != nil {
		return err
	}
	var err error
	if v := query.Get("from"); v != "" {
		filter.From, err = time.Parse(time.RFC3339, v)
		if err != nil {
			return fail(http.StatusBadRequest, "from", "Data inválida.")
		}
	}
	if v := query.Get("to"); v != "" {
		filter.To, err = time.Parse(time.RFC3339, v)
		if err != nil {
			return fail(http.StatusBadRequest, "to", "Data inválida.")
		}
	}
	return nil
}

func restoreFilterDates(filter *Filter, historical bool) error {
	revision := filter.Revision
	if parts := strings.Split(revision, "|"); len(parts) == 3 && parts[0] == "b" {
		revision = parts[1]
	}
	parts := strings.Split(revision, ":")
	if len(parts) != 4 || !frozenDateRevision(parts[0], historical) {
		return nil
	}
	left, ea := strconv.ParseInt(parts[2], 10, numericBitSize)
	right, eb := strconv.ParseInt(parts[3], 10, numericBitSize)
	if ea != nil || eb != nil {
		return fail(http.StatusBadRequest, "revision", "Revisão inválida.")
	}
	filter.From, filter.To = time.Unix(0, left).UTC(), time.Unix(0, right).UTC()
	return nil
}

func frozenDateRevision(prefix string, historical bool) bool {
	if historical {
		return prefix == "s"
	}
	return prefix == "t" || prefix == "a"
}
