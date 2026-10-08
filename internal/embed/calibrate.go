package embed

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/FreePeak/LeanKG/internal/store"
)

// Noise-floor calibration. Cosine scores are not comparable across models
// (bge-small scores unrelated text around 0.5–0.6 and on-topic questions
// 0.63+), so no fixed similarity floor is right. Instead every embed run
// measures how close text that cannot be about the corpus gets to it: the
// best match of a fixed set of off-topic probes is the collection's noise
// floor. A semantic answer whose best hit does not beat it is flagged
// low-confidence (the hits are still returned), and dense memory recall only
// admits rows above it, so an unrelated question recalls nothing.

// calibrationProbes are sentences no code or engineering-notes corpus is
// about. They are a fixed list so the floor is reproducible per collection.
var calibrationProbes = []string{
	"plan a two week vacation in japan",
	"how much water should a person drink every day",
	"lyrics of a popular summer song",
	"the rules of chess for beginners",
	"how to make cold brew coffee at home",
	"best exercises for lower back pain",
	"how do volcanoes form",
	"translate good morning into spanish",
	"a short poem about autumn leaves",
	"how to remove a red wine stain from a carpet",
	"the life cycle of a butterfly",
	"which flowers grow best in shade",
}

// calibrationNS is the store KV namespace; the key is the model id.
const calibrationNS = "embed_calibration"

// Calibration is one collection's measured noise floor, bound to the
// collection identity it was measured on.
type Calibration struct {
	Floor    float64 `json:"floor"`
	Identity string  `json:"identity"`
}

// identityOf binds a calibration to everything that shapes the vectors.
func identityOf(s store.ModelStamp) string {
	return fmt.Sprintf("%s|%s|%d|%d|%s|%s", s.Revision, s.Provider, s.Dimensions, s.ChunkerVersion, s.QueryPrefix, s.DocumentPrefix)
}

// Calibrate measures and stores the noise floor of p's collection: the best
// similarity any off-topic probe reaches. It returns 0, false when the
// collection is empty.
func Calibrate(ctx context.Context, st store.Backend, p Provider) (float64, bool, error) {
	vecs, err := p.Embed(ctx, Query, calibrationProbes)
	if err != nil {
		return 0, false, fmt.Errorf("embed: calibration probes: %w", err)
	}
	floor, found := 0.0, false
	for _, v := range vecs {
		hits, err := st.SearchVectors(p.ModelID(), v, 1)
		if err != nil {
			return 0, false, fmt.Errorf("embed: calibration search: %w", err)
		}
		if len(hits) > 0 && (!found || hits[0].Similarity > floor) {
			floor, found = hits[0].Similarity, true
		}
	}
	if !found {
		return 0, false, nil
	}
	if err := WriteCalibration(st, StampOf(p), floor); err != nil {
		return 0, false, err
	}
	return floor, true, nil
}

// WriteCalibration stores a noise floor for the collection stamp.
func WriteCalibration(st store.Backend, stamp store.ModelStamp, floor float64) error {
	raw, _ := json.Marshal(Calibration{Floor: floor, Identity: identityOf(stamp)})
	return st.KVSet(calibrationNS, stamp.ModelID, string(raw))
}

// ReadCalibration returns the stored noise floor for the collection stamp,
// or false when none was measured for exactly this identity.
func ReadCalibration(st store.Backend, stamp store.ModelStamp) (float64, bool) {
	raw, ok, err := st.KVGet(calibrationNS, stamp.ModelID)
	if err != nil || !ok {
		return 0, false
	}
	var c Calibration
	if json.Unmarshal([]byte(raw), &c) != nil || c.Identity != identityOf(stamp) {
		return 0, false
	}
	return c.Floor, true
}
