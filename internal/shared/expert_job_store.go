package shared

// ExpertJobStoreKind identifies an expert-job stall kind in shared
// orchestration. Wire kind bytes stay in the adapter's protocol package.
type ExpertJobStoreKind int

const (
	ExpertJobStoreNone ExpertJobStoreKind = iota
	ExpertJobStoreDisjoint
	ExpertJobStoreEnchant
)

// Name returns a stable lowercase name for logs and API payloads.
func (k ExpertJobStoreKind) Name() string {
	switch k {
	case ExpertJobStoreDisjoint:
		return "disjoint"
	case ExpertJobStoreEnchant:
		return "enchant"
	default:
		return "none"
	}
}
