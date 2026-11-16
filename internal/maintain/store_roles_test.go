package maintain

import (
	"context"
	"testing"

	"github.com/FreePeak/LeanKG/internal/store"
)

// inventoryFake embeds the package Store as nil; only the watermark and the
// inventory read are real. A fresh inventory must not trigger a refresh, which
// would call the element and vector roles that are nil here.
type inventoryFake struct {
	Store
	seq int64
	inv *store.Inventory
}

func (f *inventoryFake) Watermark(_ context.Context) (int64, int64, error) {
	return f.seq, 0, nil
}

func (f *inventoryFake) LoadInventory(_ context.Context) (*store.Inventory, error) {
	return f.inv, nil
}

func TestHealInventoryFreshNeedsOnlyInventoryRoles(t *testing.T) {
	f := &inventoryFake{seq: 7, inv: &store.Inventory{LastInventorySeq: 7}}
	if err := healInventory(context.Background(), f); err != nil {
		t.Fatalf("healInventory: %v", err)
	}
}
