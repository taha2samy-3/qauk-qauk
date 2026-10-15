package elementpipe

import (
	"fmt"
	"sort"
	"strings"
)

// unitDef converts to the dimension's base unit: base = v·factor + offset.
type unitDef struct {
	dim     string
	factor  float64
	offset  float64
	aliases []string
}

// units by canonical name. Conversions work between any two units of the
// same dimension.
var units = map[string]unitDef{
	// temperature (base K)
	"C": {"temperature", 1, 273.15, []string{"degC", "°C", "celsius"}},
	"F": {"temperature", 5.0 / 9.0, 273.15 - 32*5.0/9.0, []string{"degF", "°F", "fahrenheit"}},
	"K": {"temperature", 1, 0, []string{"kelvin"}},
	// pressure (base Pa)
	"Pa":   {"pressure", 1, 0, nil},
	"hPa":  {"pressure", 100, 0, nil},
	"kPa":  {"pressure", 1e3, 0, nil},
	"MPa":  {"pressure", 1e6, 0, nil},
	"mbar": {"pressure", 100, 0, nil},
	"bar":  {"pressure", 1e5, 0, nil},
	"psi":  {"pressure", 6894.757293168361, 0, nil},
	"atm":  {"pressure", 101325, 0, nil},
	// speed (base m/s)
	"m/s":  {"speed", 1, 0, nil},
	"km/h": {"speed", 1 / 3.6, 0, []string{"kmh"}},
	"mph":  {"speed", 0.44704, 0, nil},
	"kn":   {"speed", 1852.0 / 3600, 0, []string{"knot"}},
	// energy (base J)
	"J":   {"energy", 1, 0, nil},
	"kJ":  {"energy", 1e3, 0, nil},
	"MJ":  {"energy", 1e6, 0, nil},
	"Wh":  {"energy", 3600, 0, nil},
	"kWh": {"energy", 3.6e6, 0, nil},
	"MWh": {"energy", 3.6e9, 0, nil},
	// power (base W)
	"W":  {"power", 1, 0, nil},
	"kW": {"power", 1e3, 0, nil},
	"MW": {"power", 1e6, 0, nil},
	// length (base m)
	"mm": {"length", 1e-3, 0, nil},
	"cm": {"length", 1e-2, 0, nil},
	"m":  {"length", 1, 0, nil},
	"km": {"length", 1e3, 0, nil},
	"in": {"length", 0.0254, 0, nil},
	"ft": {"length", 0.3048, 0, nil},
	"mi": {"length", 1609.344, 0, nil},
	// volume (base L)
	"mL":  {"volume", 1e-3, 0, nil},
	"L":   {"volume", 1, 0, []string{"l"}},
	"m3":  {"volume", 1e3, 0, []string{"m³"}},
	"gal": {"volume", 3.785411784, 0, nil},
	"ft3": {"volume", 28.316846592, 0, nil},
	// flow (base L/s)
	"L/s":   {"flow", 1, 0, nil},
	"L/min": {"flow", 1.0 / 60, 0, nil},
	"m3/h":  {"flow", 1e3 / 3600, 0, []string{"m³/h"}},
	// mass (base kg)
	"g":  {"mass", 1e-3, 0, nil},
	"kg": {"mass", 1, 0, nil},
	"t":  {"mass", 1e3, 0, nil},
	"lb": {"mass", 0.45359237, 0, nil},
	"oz": {"mass", 0.028349523125, 0, nil},
	// time (base s)
	"ms":  {"time", 1e-3, 0, nil},
	"s":   {"time", 1, 0, nil},
	"min": {"time", 60, 0, nil},
	"h":   {"time", 3600, 0, nil},
	// angle (base rad)
	"rad": {"angle", 1, 0, nil},
	"deg": {"angle", 3.14159265358979323846 / 180, 0, []string{"°"}},
	// frequency (base Hz)
	"Hz":  {"frequency", 1, 0, nil},
	"kHz": {"frequency", 1e3, 0, nil},
	"MHz": {"frequency", 1e6, 0, nil},
}

var unitAliases = func() map[string]string {
	out := map[string]string{}
	for name, u := range units {
		out[name] = name
		for _, a := range u.aliases {
			out[a] = name
		}
	}
	return out
}()

// UnitInfo describes one unit for UIs.
type UnitInfo struct {
	Name      string   `json:"name"`
	Aliases   []string `json:"aliases,omitempty"`
	Dimension string   `json:"dimension"`
}

// Units lists the units the unit step knows, by dimension then name.
func Units() []UnitInfo {
	out := make([]UnitInfo, 0, len(units))
	for name, u := range units {
		out = append(out, UnitInfo{Name: name, Aliases: u.aliases, Dimension: u.dim})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Dimension != out[j].Dimension {
			return out[i].Dimension < out[j].Dimension
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// KnownUnits lists the canonical unit names.
func KnownUnits() []string {
	out := make([]string, 0, len(units))
	for _, u := range Units() {
		out = append(out, u.Name)
	}
	return out
}

// conversion returns from→to and to→from for two units of one dimension.
func conversion(from, to string) (fwd, rev func(float64) float64, err error) {
	fn, ok := unitAliases[strings.TrimSpace(from)]
	if !ok {
		return nil, nil, fmt.Errorf("unknown unit %q", from)
	}
	tn, ok := unitAliases[strings.TrimSpace(to)]
	if !ok {
		return nil, nil, fmt.Errorf("unknown unit %q", to)
	}
	f, t := units[fn], units[tn]
	if f.dim != t.dim {
		return nil, nil, fmt.Errorf("can't convert %s (%s) to %s (%s)", fn, f.dim, tn, t.dim)
	}
	if fn == tn {
		return nil, nil, fmt.Errorf("from and to are the same unit (%s)", fn)
	}
	fwd = func(v float64) float64 { return ((v*f.factor + f.offset) - t.offset) / t.factor }
	rev = func(v float64) float64 { return ((v*t.factor + t.offset) - f.offset) / f.factor }
	return fwd, rev, nil
}
