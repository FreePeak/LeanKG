package archtest

import (
	"context"
	"reflect"
	"sort"
	"testing"

	"github.com/FreePeak/LeanKG/internal/store"
)

// ctxlessBackendMethods lists every store.Backend method whose first parameter
// is not context.Context. The list starts as the full unmigrated surface
// (106 methods) and must shrink with each store-role migration (plan CX-19..CX-27).
//
// Close, Path, Engine and Mode are permanent exemptions: they are not I/O
// roles and keep no ctx. Every other name must leave the list in the PR that
// migrates its role. A name that is already compliant or no longer exists
// fails the test, so the list can only shrink.
var ctxlessBackendMethods = []string{
	"AccountByEmail",
	"AccountUpsert",
	"AdminExists",
	"AppendAudit",
	"AuditTail",
	"BumpWatermark",
	"Checkpoint",
	"CleanupMetrics",
	"ClearVectors",
	"Close",
	"CompactFTS",
	"DeleteByFile",
	"DeleteFileRecord",
	"DeleteOrphanRelationships",
	"DeleteOrphanVectors",
	"DeleteStaleStamps",
	"ElementCount",
	"Elements",
	"ElementsByType",
	"EmbeddingStateMap",
	"Engine",
	"EnsureIncrementalVacuum",
	"EnvSnapshotConflictsWith",
	"EnvSnapshotEnvVariants",
	"EnvSnapshotGet",
	"EnvSnapshotsPut",
	"FileCount",
	"Files",
	"FindByNameToken",
	"FindExact",
	"FindFuzzy",
	"FinishEmbedRun",
	"IncidentByID",
	"IncidentDelete",
	"IncidentUpsert",
	"IncidentsQuery",
	"Incoming",
	"IncrementalVacuum",
	"IsResourceOwner",
	"KVGet",
	"KVSet",
	"KnowledgeEntriesByElement",
	"KnowledgeEntriesByEnvironment",
	"KnowledgeEntriesByFeature",
	"KnowledgeEntriesSearch",
	"KnowledgeEntryByID",
	"KnowledgeEntryDelete",
	"KnowledgeEntryUpsert",
	"LastEmbedRun",
	"LastEmbedRunAny",
	"LoadInventory",
	"MetricsSummary",
	"Migrate",
	"OrgByID",
	"OrgMemberOf",
	"OrgMembers",
	"OrgMembershipUpsert",
	"OrgUpsert",
	"OrgsByOwner",
	"Outgoing",
	"Path",
	"ProjectForget",
	"ProjectGet",
	"ProjectList",
	"ProjectUpsert",
	"PruneEmbedRuns",
	"RecordMetric",
	"RelationshipCount",
	"RelationshipsAll",
	"ReplaceFileVectors",
	"ResetMetrics",
	"ResourceClaim",
	"SaveInventory",
	"SearchVectors",
	"SearchVectorsScoped",
	"ServiceMetadataAll",
	"ServiceMetadataGet",
	"ServiceMetadataUpsert",
	"SetEmbeddingStates",
	"Space",
	"Stamp",
	"Stamps",
	"StartEmbedRun",
	"SummariesAll",
	"SummaryGet",
	"SummaryUpsert",
	"TeamMemberOf",
	"TeamMemberUpsert",
	"TeamMembers",
	"TokenDelete",
	"TokenFind",
	"TokenList",
	"TokenRevoke",
	"TokenTouch",
	"TokenUpsert",
	"UpsertElements",
	"UpsertFiles",
	"UpsertRelationships",
	"UpsertVectors",
	"UsageAggregates",
	"Vacuum",
	"VectorCount",
	"VectorCoverage",
	"VerifyAuditChain",
	"Watermark",
	"WriteStamp",
}

// TestStoreRolesTakeContext ratchets store.Backend toward ctx-first methods.
// A method not on the allowlist must take context.Context as its first
// parameter; an allowlisted method that is compliant or missing is stale.
func TestStoreRolesTakeContext(t *testing.T) {
	ctxType := reflect.TypeOf((*context.Context)(nil)).Elem()
	backend := reflect.TypeOf((*store.Backend)(nil)).Elem()

	nonCompliant := map[string]bool{}
	for i := 0; i < backend.NumMethod(); i++ {
		m := backend.Method(i)
		if m.Type.NumIn() > 0 && m.Type.In(0) == ctxType {
			continue
		}
		nonCompliant[m.Name] = true
	}

	allowed := map[string]bool{}
	for _, name := range ctxlessBackendMethods {
		allowed[name] = true
	}

	var newNames []string
	for name := range nonCompliant {
		if !allowed[name] {
			newNames = append(newNames, name)
		}
	}
	sort.Strings(newNames)
	for _, name := range newNames {
		t.Errorf("store.Backend.%s has no context.Context first parameter: add ctx context.Context as the first parameter", name)
	}

	for _, name := range ctxlessBackendMethods {
		if !nonCompliant[name] {
			t.Errorf("ctxlessBackendMethods lists %s, which is ctx-compliant or no longer exists: remove it from internal/archtest/roles_ctx_test.go", name)
		}
	}
}
