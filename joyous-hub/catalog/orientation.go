package catalog

// OrientationFromDimensions returns landscape, portrait, or square.
func OrientationFromDimensions(width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	if width > height {
		return "landscape"
	}
	if height > width {
		return "portrait"
	}
	return "square"
}
