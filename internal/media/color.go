package media

import (
	"image/color"
	"math"
)

type oklchColor struct {
	L float64
	C float64
	H float64
}

func colorToOKLCH(source color.NRGBA) oklchColor {
	red := srgbToLinear(float64(source.R) / 255)
	green := srgbToLinear(float64(source.G) / 255)
	blue := srgbToLinear(float64(source.B) / 255)

	l := math.Cbrt(0.4122214708*red + 0.5363325363*green + 0.0514459929*blue)
	m := math.Cbrt(0.2119034982*red + 0.6806995451*green + 0.1073969566*blue)
	s := math.Cbrt(0.0883024619*red + 0.2817188376*green + 0.6299787005*blue)

	lightness := 0.2104542553*l + 0.7936177850*m - 0.0040720468*s
	a := 1.9779984951*l - 2.4285922050*m + 0.4505937099*s
	b := 0.0259040371*l + 0.7827717662*m - 0.8086757660*s
	hue := math.Atan2(b, a)
	if hue < 0 {
		hue += 2 * math.Pi
	}
	return oklchColor{L: lightness, C: math.Hypot(a, b), H: hue}
}

func oklchToColor(source oklchColor) color.NRGBA {
	source.L = clamp(source.L, 0, 1)
	source.C = max(0, source.C)
	var red, green, blue float64
	for range 25 {
		red, green, blue = oklchToLinearRGB(source)
		if inUnitRange(red) && inUnitRange(green) && inUnitRange(blue) {
			break
		}
		source.C *= 0.9
	}
	return color.NRGBA{
		R: uint8(math.Round(255 * linearToSRGB(clamp(red, 0, 1)))),
		G: uint8(math.Round(255 * linearToSRGB(clamp(green, 0, 1)))),
		B: uint8(math.Round(255 * linearToSRGB(clamp(blue, 0, 1)))),
		A: 255,
	}
}

func oklchToLinearRGB(source oklchColor) (float64, float64, float64) {
	a := source.C * math.Cos(source.H)
	b := source.C * math.Sin(source.H)
	l := source.L + 0.3963377774*a + 0.2158037573*b
	m := source.L - 0.1055613458*a - 0.0638541728*b
	s := source.L - 0.0894841775*a - 1.2914855480*b
	l = l * l * l
	m = m * m * m
	s = s * s * s
	return 4.0767416621*l - 3.3077115913*m + 0.2309699292*s,
		-1.2684380046*l + 2.6097574011*m - 0.3413193965*s,
		-0.0041960863*l - 0.7034186147*m + 1.7076147010*s
}

func interpolateOKLCH(first, second oklchColor, amount float64) oklchColor {
	amount = clamp(amount, 0, 1)
	hue := first.H
	if first.C < 0.005 {
		hue = second.H
	} else if second.C >= 0.005 {
		hue += shortestHueDelta(first.H, second.H) * amount
	}
	return oklchColor{
		L: first.L + (second.L-first.L)*amount,
		C: first.C + (second.C-first.C)*amount,
		H: normalizeHue(hue),
	}
}

func oklabDistance(first, second oklchColor) float64 {
	firstA, firstB := first.C*math.Cos(first.H), first.C*math.Sin(first.H)
	secondA, secondB := second.C*math.Cos(second.H), second.C*math.Sin(second.H)
	return math.Sqrt(
		math.Pow(first.L-second.L, 2) +
			math.Pow(firstA-secondA, 2) +
			math.Pow(firstB-secondB, 2),
	)
}

func shortestHueDelta(first, second float64) float64 {
	difference := normalizeHue(second) - normalizeHue(first)
	if difference > math.Pi {
		difference -= 2 * math.Pi
	} else if difference < -math.Pi {
		difference += 2 * math.Pi
	}
	return difference
}

func normalizeHue(value float64) float64 {
	value = math.Mod(value, 2*math.Pi)
	if value < 0 {
		value += 2 * math.Pi
	}
	return value
}

func relativeLuminance(source color.NRGBA) float64 {
	return 0.2126*srgbToLinear(float64(source.R)/255) +
		0.7152*srgbToLinear(float64(source.G)/255) +
		0.0722*srgbToLinear(float64(source.B)/255)
}

func smoothstep(edge0, edge1, value float64) float64 {
	if edge0 == edge1 {
		return 0
	}
	position := clamp((value-edge0)/(edge1-edge0), 0, 1)
	return position * position * (3 - 2*position)
}

func srgbToLinear(value float64) float64 {
	if value <= 0.04045 {
		return value / 12.92
	}
	return math.Pow((value+0.055)/1.055, 2.4)
}

func linearToSRGB(value float64) float64 {
	if value <= 0.0031308 {
		return 12.92 * value
	}
	return 1.055*math.Pow(value, 1/2.4) - 0.055
}

func inUnitRange(value float64) bool {
	return value >= -1e-7 && value <= 1+1e-7
}

func clamp(value, lower, upper float64) float64 {
	return max(lower, min(upper, value))
}
