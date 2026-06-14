package adapters

import (
	"github.com/alexmorbo/seasonfill/application/scan"
	"github.com/alexmorbo/seasonfill/interface/http/handlers"
)

// RegrabInstanceRegistry adapts handlers.InstanceRegistry to the
// application/regrab.InstanceRegistry interface. The Get(name) →
// (scan.Instance, bool) semantics are a thin nil-safe wrapper.
type RegrabInstanceRegistry struct {
	Reg handlers.InstanceRegistry
}

// NewRegrabInstanceRegistry wraps the supplied registry.
func NewRegrabInstanceRegistry(reg handlers.InstanceRegistry) RegrabInstanceRegistry {
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
