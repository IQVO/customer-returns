package architecture

import (
	"testing"

	archgo "github.com/arch-go/arch-go/api"
	"github.com/arch-go/arch-go/api/configuration"
)

// The analytics read side (ADR 0012) is a region of its own: a pure domain
// package (internal/domain/returnsperformance), an analytical-store adapter and
// two binaries. These rules keep it derived FROM the event stream and never
// dual-written from, or entangled with, the transactional core.
func TestAnalyticsReadSideArchitecture(t *testing.T) {
	moduleInfo := configuration.Load(modulePath)

	t.Run("the returns-performance read model imports no other internal package", func(t *testing.T) {
		// The facts, the report shapes, the KPI rules, the range rules and the
		// writer/reader ports. Not the ReturnAuthorization aggregate: the event
		// vocabulary is hand-mirrored from apis/asyncapi.yaml, so the OLTP
		// model can never leak into the projection.
		rule := &configuration.DependenciesRule{
			Package: "**.internal.domain.returnsperformance.**",
			ShouldOnlyDependsOn: &configuration.Dependencies{
				Internal: []string{"**.internal.domain.returnsperformance.**"},
			},
		}
		assertPass(t, archgo.CheckArchitecture(moduleInfo, configuration.Config{DependenciesRules: []*configuration.DependenciesRule{rule}}))
	})

	t.Run("the analytical store depends only on the read model and the analytical schema", func(t *testing.T) {
		rule := &configuration.DependenciesRule{
			Package: "**.internal.adapters.outbound.analyticsstore.**",
			ShouldOnlyDependsOn: &configuration.Dependencies{
				Internal: []string{
					"**.internal.domain.returnsperformance.**",
					"**.internal.adapters.outbound.analyticsstore.**",
					"**.customer-returns.analytics",
				},
			},
		}
		assertPass(t, archgo.CheckArchitecture(moduleInfo, configuration.Config{DependenciesRules: []*configuration.DependenciesRule{rule}}))
	})

	t.Run("the OLTP side never imports the analytics store or read model", func(t *testing.T) {
		forbidden := &configuration.Dependencies{
			Internal: []string{"**.internal.domain.returnsperformance.**", "**.internal.adapters.outbound.analyticsstore.**"},
		}
		rules := []*configuration.DependenciesRule{
			{Package: "**.internal.domain.returnauthorization.**", ShouldNotDependsOn: forbidden},
			{Package: "**.internal.domain.network.**", ShouldNotDependsOn: forbidden},
			{Package: "**.internal.domain.shared.**", ShouldNotDependsOn: forbidden},
			{Package: "**.internal.application.**", ShouldNotDependsOn: forbidden},
			{Package: "**.internal.adapters.outbound.postgres.**", ShouldNotDependsOn: forbidden},
		}
		assertPass(t, archgo.CheckArchitecture(moduleInfo, configuration.Config{DependenciesRules: rules}))
	})
}
