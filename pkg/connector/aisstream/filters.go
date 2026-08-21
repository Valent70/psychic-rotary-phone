package aisstream

// passesFilters applies Config's bounding-box, MMSI, and message-type
// filters (in that priority order for the reported reason) to a
// decoded, non-heartbeat message. ok is true only if every configured
// filter (an empty filter is "no restriction") admits w; reason is a
// short machine-readable string naming the filter that excluded it,
// meaningful only when ok is false.
func (c *Connector) passesFilters(w wireMessage) (reason string, ok bool) {
	if len(c.cfg.MessageTypeFilter) > 0 && !stringInSlice(c.cfg.MessageTypeFilter, w.MessageType) {
		return "message_type_not_in_filter", false
	}
	if len(c.cfg.MMSIFilter) > 0 {
		if w.MMSI == nil || !int64InSlice(c.cfg.MMSIFilter, *w.MMSI) {
			return "mmsi_not_in_filter", false
		}
	}
	if len(c.cfg.BoundingBoxes) > 0 {
		if w.Latitude == nil || w.Longitude == nil {
			return "no_position_for_bounding_box_filter", false
		}
		if !anyBoxContains(c.cfg.BoundingBoxes, *w.Latitude, *w.Longitude) {
			return "outside_bounding_boxes", false
		}
	}
	return "", true
}

func stringInSlice(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

func int64InSlice(list []int64, v int64) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

func anyBoxContains(boxes []BoundingBox, lat, lon float64) bool {
	for _, box := range boxes {
		if boxContains(box, lat, lon) {
			return true
		}
	}
	return false
}

// boxContains reports whether (lat, lon) falls within box, normalizing
// each pair's min/max so either diagonal corner order works.
func boxContains(box BoundingBox, lat, lon float64) bool {
	minLat, maxLat := box[0][0], box[1][0]
	if minLat > maxLat {
		minLat, maxLat = maxLat, minLat
	}
	minLon, maxLon := box[0][1], box[1][1]
	if minLon > maxLon {
		minLon, maxLon = maxLon, minLon
	}
	return lat >= minLat && lat <= maxLat && lon >= minLon && lon <= maxLon
}
