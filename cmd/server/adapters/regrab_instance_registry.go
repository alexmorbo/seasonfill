package adapters

import (
	"github.com/alexmorbo/seasonfill/internal/catalog/app/scan"
	catalogrest "github.com/alexmorbo/seasonfill/internal/catalog/rest"
)

// RegrabInstanceRegistry adapts catalogrest.InstanceRegistry to the
// application/regrab.InstanceRegistry interface. The Get(name) →
// (scan.Instance, bool) semantics are a thin nil-safe wrapper.
type RegrabInstanceRegistry struct {
	Reg catalogrest.InstanceRegistry
}

// NewRegrabInstanceRegistry wraps the supplied registry.
func NewRegrabInstanceRegistry(reg catalogrest.InstanceRegistry) RegrabInstanceRegistry {
	return RegrabInstanceRegistry{Reg: reg}
}

// Get implements application/regrab.InstanceRegistry.
func (r RegrabInstanceRegistry) Get(name string) (scan.Instance, bool) {
	if r.Reg.Load == nil {
		return scan.Instance{}, false
	}
	inst, ok := r.Reg.Load()[name]
	return inst, ok
}
