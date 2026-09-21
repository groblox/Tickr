// Package astro computes equinoxes, solstices and moon phases using the
// algorithms from Jean Meeus, "Astronomical Algorithms" (chapters 27 and 49).
// Results are accurate to a few minutes, far better than a printout needs.
package astro

import (
	"math"
	"time"
)

// Season events.
type Season int

// Season identifiers in calendar order.
const (
	MarchEquinox Season = iota
	JuneSolstice
	SeptemberEquinox
	DecemberSolstice
)

func (s Season) String() string {
	switch s {
	case MarchEquinox:
		return "March equinox"
	case JuneSolstice:
		return "June solstice"
	case SeptemberEquinox:
		return "September equinox"
	default:
		return "December solstice"
	}
}

// Name returns the northern-hemisphere season that starts at the event.
func (s Season) Name(northern bool) string {
	names := []string{"spring", "summer", "autumn", "winter"}
	if !northern {
		names = []string{"autumn", "winter", "spring", "summer"}
	}
	return names[s]
}

var seasonTerms = [][3]float64{
	{485, 324.96, 1934.136}, {203, 337.23, 32964.467}, {199, 342.08, 20.186}, {182, 27.85, 445267.112},
	{156, 73.14, 45036.886}, {136, 171.52, 22518.443}, {77, 222.54, 65928.934}, {74, 296.72, 3034.906},
	{70, 243.58, 9037.513}, {58, 119.81, 33718.147}, {52, 297.17, 150.678}, {50, 21.02, 2281.226},
	{45, 247.54, 29929.562}, {44, 325.15, 31555.956}, {29, 60.93, 4443.417}, {18, 155.12, 67555.328},
	{17, 288.79, 4562.452}, {16, 198.04, 62894.029}, {14, 199.76, 31436.921}, {12, 95.39, 14577.848},
	{12, 287.11, 31931.756}, {12, 320.81, 34777.259}, {9, 227.73, 1222.114}, {8, 15.45, 16859.074},
}

// SeasonTime returns the instant of the given equinox or solstice in a year (UTC).
func SeasonTime(year int, s Season) time.Time {
	y := float64(year-2000) / 1000
	var jde0 float64
	switch s {
	case MarchEquinox:
		jde0 = 2451623.80984 + 365242.37404*y + 0.05169*y*y - 0.00411*y*y*y - 0.00057*y*y*y*y
	case JuneSolstice:
		jde0 = 2451716.56767 + 365241.62603*y + 0.00325*y*y + 0.00888*y*y*y - 0.00030*y*y*y*y
	case SeptemberEquinox:
		jde0 = 2451810.21715 + 365242.01767*y - 0.11575*y*y + 0.00337*y*y*y + 0.00078*y*y*y*y
	default:
		jde0 = 2451900.05952 + 365242.74049*y - 0.06223*y*y - 0.00823*y*y*y + 0.00032*y*y*y*y
	}
	t := (jde0 - 2451545.0) / 36525
	w := rad(35999.373*t - 2.47)
	dl := 1 + 0.0334*math.Cos(w) + 0.0007*math.Cos(2*w)
	var sum float64
	for _, term := range seasonTerms {
		sum += term[0] * math.Cos(rad(term[1]+term[2]*t))
	}
	return jdToTime(jde0 + 0.00001*sum/dl)
}

// Phase identifies a principal moon phase.
type Phase int

// Principal phases.
const (
	NewMoon Phase = iota
	FullMoon
)

func (p Phase) String() string {
	if p == NewMoon {
		return "New moon"
	}
	return "Full moon"
}

// PhaseTime returns the instant of the k-th phase since the year-2000 new
// moon. Integer k gives new moons; k+0.5 gives full moons.
func phaseTime(k float64, p Phase) time.Time {
	t := k / 1236.85
	jde := 2451550.09766 + 29.530588861*k + 0.00015437*t*t - 0.000000150*t*t*t + 0.00000000073*t*t*t*t
	e := 1 - 0.002516*t - 0.0000074*t*t
	m := rad(2.5534 + 29.10535670*k - 0.0000014*t*t - 0.00000011*t*t*t)
	mp := rad(201.5643 + 385.81693528*k + 0.0107582*t*t + 0.00001238*t*t*t - 0.000000058*t*t*t*t)
	f := rad(160.7108 + 390.67050284*k - 0.0016118*t*t - 0.00000227*t*t*t + 0.000000011*t*t*t*t)
	om := rad(124.7746 - 1.56375588*k + 0.0020672*t*t + 0.00000215*t*t*t)
	sin := math.Sin
	var c float64
	if p == NewMoon {
		c = -0.40720*sin(mp) + 0.17241*e*sin(m) + 0.01608*sin(2*mp) + 0.01039*sin(2*f) + 0.00739*e*sin(mp-m) -
			0.00514*e*sin(mp+m) + 0.00208*e*e*sin(2*m) - 0.00111*sin(mp-2*f) - 0.00057*sin(mp+2*f) +
			0.00056*e*sin(2*mp+m) - 0.00042*sin(3*mp) + 0.00042*e*sin(m+2*f) + 0.00038*e*sin(m-2*f) -
			0.00024*e*sin(2*mp-m) - 0.00017*sin(om) - 0.00007*sin(mp+2*m) + 0.00004*sin(2*mp-2*f) +
			0.00004*sin(3*m) + 0.00003*sin(mp+m-2*f) + 0.00003*sin(2*mp+2*f) - 0.00003*sin(mp+m+2*f) +
			0.00003*sin(mp-m+2*f) - 0.00002*sin(mp-m-2*f) - 0.00002*sin(3*mp+m) + 0.00002*sin(4*mp)
	} else {
		c = -0.40614*sin(mp) + 0.17302*e*sin(m) + 0.01614*sin(2*mp) + 0.01043*sin(2*f) + 0.00734*e*sin(mp-m) -
			0.00515*e*sin(mp+m) + 0.00209*e*e*sin(2*m) - 0.00111*sin(mp-2*f) - 0.00057*sin(mp+2*f) +
			0.00056*e*sin(2*mp+m) - 0.00042*sin(3*mp) + 0.00042*e*sin(m+2*f) + 0.00038*e*sin(m-2*f) -
			0.00024*e*sin(2*mp-m) - 0.00017*sin(om) - 0.00007*sin(mp+2*m) + 0.00004*sin(2*mp-2*f) +
			0.00004*sin(3*m) + 0.00003*sin(mp+m-2*f) + 0.00003*sin(2*mp+2*f) - 0.00003*sin(mp+m+2*f) +
			0.00003*sin(mp-m+2*f) - 0.00002*sin(mp-m-2*f) - 0.00002*sin(3*mp+m) + 0.00002*sin(4*mp)
	}
	return jdToTime(jde + c)
}

// MoonEvent is one new or full moon.
type MoonEvent struct {
	Phase Phase
	Time  time.Time
}

// MoonPhases returns the new and full moons between from and to, in order.
func MoonPhases(from, to time.Time) []MoonEvent {
	yearFrac := float64(from.Year()) + float64(from.YearDay())/365.25
	k := math.Floor((yearFrac-2000)*12.3685) - 1
	var out []MoonEvent
	for i := 0; i < 400; i++ {
		kk := k + float64(i)
		nm := phaseTime(kk, NewMoon)
		fm := phaseTime(kk+0.5, FullMoon)
		if nm.After(to) && fm.After(to) {
			break
		}
		if !nm.Before(from) && !nm.After(to) {
			out = append(out, MoonEvent{NewMoon, nm})
		}
		if !fm.Before(from) && !fm.After(to) {
			out = append(out, MoonEvent{FullMoon, fm})
		}
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].Time.Before(out[j-1].Time); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// SeasonEvent is one equinox or solstice.
type SeasonEvent struct {
	Season Season
	Time   time.Time
}

// Seasons returns the equinoxes and solstices between from and to.
func Seasons(from, to time.Time) []SeasonEvent {
	var out []SeasonEvent
	for y := from.Year(); y <= to.Year(); y++ {
		for s := MarchEquinox; s <= DecemberSolstice; s++ {
			t := SeasonTime(y, s)
			if !t.Before(from) && !t.After(to) {
				out = append(out, SeasonEvent{s, t})
			}
		}
	}
	return out
}

// FullMoonName returns the traditional North American name for a full moon
// in the given month.
func FullMoonName(m time.Month) string {
	return [...]string{"", "Wolf Moon", "Snow Moon", "Worm Moon", "Pink Moon", "Flower Moon", "Strawberry Moon",
		"Buck Moon", "Sturgeon Moon", "Harvest Moon", "Hunter's Moon", "Beaver Moon", "Cold Moon"}[m]
}

func rad(deg float64) float64 { return deg * math.Pi / 180 }

func jdToTime(jd float64) time.Time {
	secs := (jd - 2440587.5) * 86400
	return time.Unix(int64(secs), int64((secs-math.Floor(secs))*1e9)).UTC()
}
