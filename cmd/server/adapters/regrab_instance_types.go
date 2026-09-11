package adapters

import (
	"github.com/alexmorbo/seasonfill/internal/catalog/app/scan"
)

// RegrabInstanceTypes resolves instance name -> arr_instance.type for the
// regrab loop's supported-type gate (ADR-0025 F2). It satisfies
// loops.InstanceTypeSource.
//
// TWO sources, because no single holder knows every instance:
//
//   - InstanceMapHolder carries ONLY sonarr rows — radarr is excluded by
//     construction at internal/wiring/catalog.go:140 (boot) and
//     internal/wiring/bootstrap.go:685 (reload fanout), the Ф6-R-4b rule.
//   - RadarrInstanceMapHolder carries ONLY radarr rows.
//
// Their union is the whole arr_instance table as of the last publish.
// Reading just one of them would make a THIRD instance type look "unknown"
// and sail straight through the gate — the exact defect ADR-0025 exists to
// stop.
//
// Both holders are swapped by the OnApplied fanout BEFORE it calls
// refreshQbitLoops (internal/wiring/bootstrap.go:730 and :734 vs :768), so
// the map this adapter returns is never staler than the settings map the
// loop is diffing against.
//
// Both fields are nil-OK and the zero value is usable: an unset source
// contributes no names, which the loop reads as "type unknown" and treats
// as SUPPORTED (fail-open). Never as unsupported — see the loop's
// unsupportedLocked godoc.
type RegrabInstanceTypes struct {
	Sonarr func() map[string]scan.Instance
	Radarr func() map[string]scan.RadarrInstance
}

// NewRegrabInstanceTypes wires the adapter from the two reload-aware
// holders. Either may be nil (minimal wirings).
func NewRegrabInstanceTypes(sonarr *InstanceMapHolder, radarr *RadarrInstanceMapHolder) RegrabInstanceTypes {
	out := RegrabInstanceTypes{}
	if sonarr != nil {
		out.Sonarr = sonarr.Load
	}
	if radarr != nil {
		out.Radarr = radarr.Load
	}
	return out
}

// InstanceTypes returns a fresh name -> type snapshot. Radarr entries are
// written second so that, in the impossible case of a name present in both
// holders, the radarr reading wins (the stricter one).
func (r RegrabInstanceTypes) InstanceTypes() map[string]string {
	out := make(map[string]string)
	if r.Sonarr != nil {
		for name, inst := range r.Sonarr() {
			out[name] = instanceTypeOrDefault(inst.Config.Type, scan.InstanceTypeSonarr)
		}
	}
	if r.Radarr != nil {
		for name, inst := range r.Radarr() {
			out[name] = instanceTypeOrDefault(inst.Config.Type, scan.InstanceTypeRadarr)
		}
	}
	return out
}

// instanceTypeOrDefault mirrors internal/runtime/snapshot.go:341-342 and
// scan.IsRadarr: an empty arr_instance.type (legacy rows / test fixtures)
// means the type the holder it came from already implies.
func instanceTypeOrDefault(t, fallback string) string {
	if t == "" {
		return fallback
	}
	return t
}
