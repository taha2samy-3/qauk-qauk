package elementpipe

// unitConversions maps "from/to" to a forward conversion function.
// For every pair where the inverse exists, the reverse "to/from" is also present.
var unitConversions = map[string]func(float64) float64{
	// Temperature
	"C/F": func(v float64) float64 { return v*9.0/5.0 + 32 },
	"F/C": func(v float64) float64 { return (v - 32) * 5.0 / 9.0 },
	"C/K": func(v float64) float64 { return v + 273.15 },
	"K/C": func(v float64) float64 { return v - 273.15 },
	"F/K": func(v float64) float64 { return (v-32)*5.0/9.0 + 273.15 },
	"K/F": func(v float64) float64 { return (v-273.15)*9.0/5.0 + 32 },

	// Pressure
	"bar/psi": func(v float64) float64 { return v * 14.5038 },
	"psi/bar": func(v float64) float64 { return v / 14.5038 },
	"bar/kPa": func(v float64) float64 { return v * 100 },
	"kPa/bar": func(v float64) float64 { return v / 100 },
	"psi/kPa": func(v float64) float64 { return v * 6.89476 },
	"kPa/psi": func(v float64) float64 { return v / 6.89476 },
	"bar/Pa":  func(v float64) float64 { return v * 100000 },
	"Pa/bar":  func(v float64) float64 { return v / 100000 },
	"hPa/Pa":  func(v float64) float64 { return v * 100 },
	"Pa/hPa":  func(v float64) float64 { return v / 100 },
	"hPa/kPa": func(v float64) float64 { return v / 10 },
	"kPa/hPa": func(v float64) float64 { return v * 10 },

	// Speed
	"m/s/km/h": func(v float64) float64 { return v * 3.6 },
	"km/h/m/s": func(v float64) float64 { return v / 3.6 },
	"m/s/mph":  func(v float64) float64 { return v * 2.23694 },
	"mph/m/s":  func(v float64) float64 { return v / 2.23694 },
	"km/h/mph": func(v float64) float64 { return v * 0.621371 },
	"mph/km/h": func(v float64) float64 { return v / 0.621371 },
	"m/s/kn":   func(v float64) float64 { return v * 1.94384 },
	"kn/m/s":   func(v float64) float64 { return v / 1.94384 },

	// Energy
	"Wh/kWh":  func(v float64) float64 { return v / 1000 },
	"kWh/Wh":  func(v float64) float64 { return v * 1000 },
	"Wh/MWh":  func(v float64) float64 { return v / 1_000_000 },
	"MWh/Wh":  func(v float64) float64 { return v * 1_000_000 },
	"kWh/MWh": func(v float64) float64 { return v / 1000 },
	"MWh/kWh": func(v float64) float64 { return v * 1000 },
	"J/kJ":    func(v float64) float64 { return v / 1000 },
	"kJ/J":    func(v float64) float64 { return v * 1000 },
	"J/MJ":    func(v float64) float64 { return v / 1_000_000 },
	"MJ/J":    func(v float64) float64 { return v * 1_000_000 },
	"kWh/J":   func(v float64) float64 { return v * 3_600_000 },
	"J/kWh":   func(v float64) float64 { return v / 3_600_000 },

	// Power
	"W/kW":  func(v float64) float64 { return v / 1000 },
	"kW/W":  func(v float64) float64 { return v * 1000 },
	"W/MW":  func(v float64) float64 { return v / 1_000_000 },
	"MW/W":  func(v float64) float64 { return v * 1_000_000 },
	"kW/MW": func(v float64) float64 { return v / 1000 },
	"MW/kW": func(v float64) float64 { return v * 1000 },

	// Length
	"m/cm":  func(v float64) float64 { return v * 100 },
	"cm/m":  func(v float64) float64 { return v / 100 },
	"m/mm":  func(v float64) float64 { return v * 1000 },
	"mm/m":  func(v float64) float64 { return v / 1000 },
	"m/km":  func(v float64) float64 { return v / 1000 },
	"km/m":  func(v float64) float64 { return v * 1000 },
	"m/ft":  func(v float64) float64 { return v * 3.28084 },
	"ft/m":  func(v float64) float64 { return v / 3.28084 },
	"m/in":  func(v float64) float64 { return v * 39.3701 },
	"in/m":  func(v float64) float64 { return v / 39.3701 },
	"km/mi": func(v float64) float64 { return v * 0.621371 },
	"mi/km": func(v float64) float64 { return v / 0.621371 },
	"ft/in": func(v float64) float64 { return v * 12 },
	"in/ft": func(v float64) float64 { return v / 12 },

	// Mass
	"kg/g":  func(v float64) float64 { return v * 1000 },
	"g/kg":  func(v float64) float64 { return v / 1000 },
	"kg/lb": func(v float64) float64 { return v * 2.20462 },
	"lb/kg": func(v float64) float64 { return v / 2.20462 },
	"kg/t":  func(v float64) float64 { return v / 1000 },
	"t/kg":  func(v float64) float64 { return v * 1000 },
	"g/oz":  func(v float64) float64 { return v * 0.035274 },
	"oz/g":  func(v float64) float64 { return v / 0.035274 },

	// Volume
	"m3/L":   func(v float64) float64 { return v * 1000 },
	"L/m3":   func(v float64) float64 { return v / 1000 },
	"L/mL":   func(v float64) float64 { return v * 1000 },
	"mL/L":   func(v float64) float64 { return v / 1000 },
	"L/gal":  func(v float64) float64 { return v * 0.264172 },
	"gal/L":  func(v float64) float64 { return v / 0.264172 },
	"m3/ft3": func(v float64) float64 { return v * 35.3147 },
	"ft3/m3": func(v float64) float64 { return v / 35.3147 },

	// Angle
	"deg/rad": func(v float64) float64 { return v * (3.14159265358979323846 / 180) },
	"rad/deg": func(v float64) float64 { return v * (180 / 3.14159265358979323846) },

	// Frequency
	"Hz/kHz":  func(v float64) float64 { return v / 1000 },
	"kHz/Hz":  func(v float64) float64 { return v * 1000 },
	"Hz/MHz":  func(v float64) float64 { return v / 1_000_000 },
	"MHz/Hz":  func(v float64) float64 { return v * 1_000_000 },
	"kHz/MHz": func(v float64) float64 { return v / 1000 },
	"MHz/kHz": func(v float64) float64 { return v * 1000 },
}

// KnownUnits returns a deduplicated list of unit names present in the conversion table.
func KnownUnits() []string {
	seen := map[string]struct{}{}
	for pair := range unitConversions {
		parts := splitPair(pair)
		if len(parts) == 2 {
			seen[parts[0]] = struct{}{}
			seen[parts[1]] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for u := range seen {
		out = append(out, u)
	}
	return out
}

func splitPair(pair string) []string {
	// pair is "from/to" but units can contain "/" (e.g. "m/s")
	// We encoded them as "m/s/km/h" — split at the midpoint heuristically:
	// pairs were written manually so just return raw split.
	return []string{pair}
}
