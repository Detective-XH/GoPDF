// filter_predictor_test.go — KATs and regression tests for filter_predictor.go.
//
// Tests run against applyPredictor directly (post-decompression step).
// filterMakeDict is defined in filter_test.go (same package).

package pdf

import (
	"bytes"
	"io"
	"testing"
)

// ---------------------------------------------------------------------------
// TestPredictorAbsent — no Predictor key returns rd unchanged
// ---------------------------------------------------------------------------

func TestPredictorAbsent(t *testing.T) {
	src := bytes.NewReader([]byte{0x01, 0x02, 0x03})

	// param with no Predictor key
	param := filterMakeDict(map[string]any{
		"Columns": int64(3),
	})

	rd, err := applyPredictor(src, param)
	if err != nil {
		t.Fatalf("applyPredictor absent: %v", err)
	}

	got, err := io.ReadAll(rd)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	want := []byte{0x01, 0x02, 0x03}
	if !bytes.Equal(got, want) {
		t.Fatalf("absent predictor: got %v, want %v", got, want)
	}
}

// TestPredictorOne — Predictor=1 returns rd unchanged
func TestPredictorOne(t *testing.T) {
	src := bytes.NewReader([]byte{0xAA, 0xBB})
	param := filterMakeDict(map[string]any{
		"Predictor": int64(1),
	})
	rd, err := applyPredictor(src, param)
	if err != nil {
		t.Fatalf("applyPredictor 1: %v", err)
	}
	got, err := io.ReadAll(rd)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !bytes.Equal(got, []byte{0xAA, 0xBB}) {
		t.Fatalf("predictor 1: got %v", got)
	}
}

// ---------------------------------------------------------------------------
// TestPredictorPNGUpCompat — compatibility pin with TestFlateDecodePNGUp
//
// The stream from TestFlateDecodePNGUp:
//   Columns=2, rows [{2,10,20},{2,3,5}]
// Must decode to {10,20,13,25} through applyPredictor directly (no zlib).
// ---------------------------------------------------------------------------

func TestPredictorPNGUpCompat(t *testing.T) {
	// Rows: filter-type byte + delta bytes.
	// Row 0: filter=2 (Up), data=[10,20] → decoded = prior[0..1] + [10,20]
	//        prior=[0,0], so decoded=[10,20]
	// Row 1: filter=2 (Up), data=[ 3, 5] → decoded = [10,20] + [3,5] = [13,25]
	rawRows := []byte{
		2, 10, 20,
		2, 3, 5,
	}
	param := filterMakeDict(map[string]any{
		"Predictor": int64(12),
		"Columns":   int64(2),
	})

	rd, err := applyPredictor(bytes.NewReader(rawRows), param)
	if err != nil {
		t.Fatalf("applyPredictor PNG-Up compat: %v", err)
	}
	got, err := io.ReadAll(rd)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	want := []byte{10, 20, 13, 25}
	if !bytes.Equal(got, want) {
		t.Fatalf("PNG-Up compat: got %v, want %v", got, want)
	}
}

// ---------------------------------------------------------------------------
// TestPredictorPNGMixedRows — rows use filter types 0,1,2,3,4 in sequence
//
// This is the regression for pngUpReader which rejected any row type != 2.
// Columns=3, Colors=1, bpc=8 → rowBytes=3, bytesPerPixel=1.
//
// Hand-computed reconstruction:
//
//   prior (initial) = [0, 0, 0]
//
//   Row 0: filterType=0 (None), raw=[5, 10, 15]
//     decoded = [5, 10, 15]
//     prior   = [5, 10, 15]
//
//   Row 1: filterType=1 (Sub), raw=[10, 5, 5]
//     i=0: 10 + left(0, absent) = 10
//     i=1: 5  + row[0]=10       = 15
//     i=2: 5  + row[1]=15       = 20
//     decoded = [10, 15, 20]
//     prior   = [10, 15, 20]
//
//   Row 2: filterType=2 (Up), raw=[1, 1, 1]
//     decoded = [10+1, 15+1, 20+1] = [11, 16, 21]
//     prior   = [11, 16, 21]
//
//   Row 3: filterType=3 (Average), raw=[2, 4, 6]
//     i=0: left=0    up=11  avg=floor((0+11)/2)=5   → 2+5=7
//     i=1: left=7    up=16  avg=floor((7+16)/2)=11  → 4+11=15
//     i=2: left=15   up=21  avg=floor((15+21)/2)=18 → 6+18=24
//     decoded = [7, 15, 24]
//     prior   = [7, 15, 24]
//
//   Row 4: filterType=4 (Paeth), raw=[1, 2, 3]
//     i=0: a=0  b=7   c=0   p=7   pa=7  pb=0  pc=7  → pick b=7   → 1+7=8
//     i=1: a=8  b=15  c=7   p=16  pa=8  pb=1  pc=9  → pick b=15  → 2+15=17
//     i=2: a=17 b=24  c=15  p=26  pa=9  pb=2  pc=11 → pick b=24  → 3+24=27
//     decoded = [8, 17, 27]
// ---------------------------------------------------------------------------

func TestPredictorPNGMixedRows(t *testing.T) {
	rawStream := []byte{
		0, 5, 10, 15, // row 0: None
		1, 10, 5, 5, // row 1: Sub
		2, 1, 1, 1, // row 2: Up
		3, 2, 4, 6, // row 3: Average
		4, 1, 2, 3, // row 4: Paeth
	}
	param := filterMakeDict(map[string]any{
		"Predictor": int64(12),
		"Columns":   int64(3),
		"Colors":    int64(1),
	})

	rd, err := applyPredictor(bytes.NewReader(rawStream), param)
	if err != nil {
		t.Fatalf("applyPredictor mixed rows: %v", err)
	}
	got, err := io.ReadAll(rd)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	want := []byte{
		5, 10, 15, // None
		10, 15, 20, // Sub
		11, 16, 21, // Up
		7, 15, 24, // Average
		8, 17, 27, // Paeth
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("mixed rows: got %v, want %v", got, want)
	}
}

// ---------------------------------------------------------------------------
// TestPredictorPNGSub — Sub filter KAT
//
// Columns=3, Colors=1, bpc=8, bytesPerPixel=1
// filter=1, raw=[10, 5, 5]
//   i=0: 10 + left(absent,0) = 10
//   i=1: 5  + row[0]=10      = 15
//   i=2: 5  + row[1]=15      = 20
// decoded = [10, 15, 20]
// ---------------------------------------------------------------------------

func TestPredictorPNGSub(t *testing.T) {
	rawStream := []byte{1, 10, 5, 5}
	param := filterMakeDict(map[string]any{
		"Predictor": int64(11),
		"Columns":   int64(3),
	})
	rd, err := applyPredictor(bytes.NewReader(rawStream), param)
	if err != nil {
		t.Fatalf("applyPredictor Sub: %v", err)
	}
	got, err := io.ReadAll(rd)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	want := []byte{10, 15, 20}
	if !bytes.Equal(got, want) {
		t.Fatalf("Sub KAT: got %v, want %v", got, want)
	}
}

// ---------------------------------------------------------------------------
// TestPredictorPNGUp — Up filter KAT
//
// Row 0: prior=[0,0,0], filter=2, raw=[10,15,20] → decoded=[10,15,20]
// Row 1: prior=[10,15,20], filter=2, raw=[1,1,1] → decoded=[11,16,21]
// ---------------------------------------------------------------------------

func TestPredictorPNGUp(t *testing.T) {
	rawStream := []byte{
		2, 10, 15, 20,
		2, 1, 1, 1,
	}
	param := filterMakeDict(map[string]any{
		"Predictor": int64(12),
		"Columns":   int64(3),
	})
	rd, err := applyPredictor(bytes.NewReader(rawStream), param)
	if err != nil {
		t.Fatalf("applyPredictor Up: %v", err)
	}
	got, err := io.ReadAll(rd)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	want := []byte{10, 15, 20, 11, 16, 21}
	if !bytes.Equal(got, want) {
		t.Fatalf("Up KAT: got %v, want %v", got, want)
	}
}

// ---------------------------------------------------------------------------
// TestPredictorPNGAverage — Average filter KAT
//
// Row 0: prior=[0,0,0], filter=3, raw=[4, 6, 9]
//   i=0: left=0  up=0  avg=0       → 4+0=4
//   i=1: left=4  up=0  avg=2       → 6+2=8
//   i=2: left=8  up=0  avg=4       → 9+4=13
//   decoded=[4,8,13]
// ---------------------------------------------------------------------------

func TestPredictorPNGAverage(t *testing.T) {
	rawStream := []byte{3, 4, 6, 9}
	param := filterMakeDict(map[string]any{
		"Predictor": int64(13),
		"Columns":   int64(3),
	})
	rd, err := applyPredictor(bytes.NewReader(rawStream), param)
	if err != nil {
		t.Fatalf("applyPredictor Average: %v", err)
	}
	got, err := io.ReadAll(rd)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	want := []byte{4, 8, 13}
	if !bytes.Equal(got, want) {
		t.Fatalf("Average KAT: got %v, want %v", got, want)
	}
}

// ---------------------------------------------------------------------------
// TestPredictorPNGPaeth — Paeth filter KAT
//
// Row 0: prior=[0,0,0], filter=4, raw=[10,5,5]
//   i=0: a=0  b=0  c=0  p=0  pa=0  pb=0  pc=0  → a=0  → 10+0=10
//   i=1: a=10 b=0  c=0  p=10 pa=0  pb=10 pc=10 → a=10 → 5+10=15
//   i=2: a=15 b=0  c=0  p=15 pa=0  pb=15 pc=15 → a=15 → 5+15=20
//   decoded=[10,15,20]
//
// Row 1: prior=[10,15,20], filter=4, raw=[1,1,1]
//   i=0: a=0  b=10 c=0  p=10 pa=10 pb=0  pc=10 → b=10 → 1+10=11
//   i=1: a=11 b=15 c=10 p=16 pa=5  pb=1  pc=6  → b=15 → 1+15=16
//   i=2: a=16 b=20 c=15 p=21 pa=5  pb=1  pc=6  → b=20 → 1+20=21
//   decoded=[11,16,21]
// ---------------------------------------------------------------------------

func TestPredictorPNGPaeth(t *testing.T) {
	rawStream := []byte{
		4, 10, 5, 5,
		4, 1, 1, 1,
	}
	param := filterMakeDict(map[string]any{
		"Predictor": int64(14),
		"Columns":   int64(3),
	})
	rd, err := applyPredictor(bytes.NewReader(rawStream), param)
	if err != nil {
		t.Fatalf("applyPredictor Paeth: %v", err)
	}
	got, err := io.ReadAll(rd)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	want := []byte{10, 15, 20, 11, 16, 21}
	if !bytes.Equal(got, want) {
		t.Fatalf("Paeth KAT: got %v, want %v", got, want)
	}
}

// ---------------------------------------------------------------------------
// TestPredictorBpp16 — PNG Sub with bpc=16, colors=1 (bytesPerPixel=2)
//
// Columns=2, Colors=1, bpc=16 → rowBytes=(2*1*16+7)/8=4, bpp=2
// filter=1 (Sub), raw=[0x00,0x0A, 0x00,0x05]
//   i<2: left absent (0)
//   i=0: 0x00 + 0 = 0x00
//   i=1: 0x0A + 0 = 0x0A
//   i>=2:
//   i=2: 0x00 + row[0]=0x00 = 0x00
//   i=3: 0x05 + row[1]=0x0A = 0x0F
//   decoded=[0x00,0x0A,0x00,0x0F]  (big-endian pixel values 10 and 15)
// ---------------------------------------------------------------------------

func TestPredictorBpp16(t *testing.T) {
	rawStream := []byte{
		1, 0x00, 0x0A, 0x00, 0x05,
	}
	param := filterMakeDict(map[string]any{
		"Predictor":        int64(11),
		"Columns":          int64(2),
		"Colors":           int64(1),
		"BitsPerComponent": int64(16),
	})
	rd, err := applyPredictor(bytes.NewReader(rawStream), param)
	if err != nil {
		t.Fatalf("applyPredictor bpp16: %v", err)
	}
	got, err := io.ReadAll(rd)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	want := []byte{0x00, 0x0A, 0x00, 0x0F}
	if !bytes.Equal(got, want) {
		t.Fatalf("bpp16 Sub KAT: got %v, want %v", got, want)
	}
}

// ---------------------------------------------------------------------------
// TestPredictorTIFF — TIFF horizontal differencing (predictor 2)
//
// Case 1: Columns=4, Colors=1, bpc=8 → rowBytes=4, bpp=1
//   raw=[1,1,1,1]
//   i=0: 1 (no left)
//   i=1: 1 + row[0]=1 = 2
//   i=2: 1 + row[1]=2 = 3
//   i=3: 1 + row[2]=3 = 4
//   decoded=[1,2,3,4]
//
// Case 2: Columns=2, Colors=2, bpc=8 → rowBytes=4, bpp=2
//   raw=[1,2,1,2]
//   i=0,1: 1,2 (no left)
//   i=2: 1 + row[0]=1 = 2
//   i=3: 2 + row[1]=2 = 4
//   decoded=[1,2,2,4]
// ---------------------------------------------------------------------------

func TestPredictorTIFF(t *testing.T) {
	t.Run("Colors=1", func(t *testing.T) {
		rawStream := []byte{1, 1, 1, 1}
		param := filterMakeDict(map[string]any{
			"Predictor": int64(2),
			"Columns":   int64(4),
			"Colors":    int64(1),
		})
		rd, err := applyPredictor(bytes.NewReader(rawStream), param)
		if err != nil {
			t.Fatalf("applyPredictor TIFF Colors=1: %v", err)
		}
		got, err := io.ReadAll(rd)
		if err != nil {
			t.Fatalf("ReadAll: %v", err)
		}
		want := []byte{1, 2, 3, 4}
		if !bytes.Equal(got, want) {
			t.Fatalf("TIFF Colors=1: got %v, want %v", got, want)
		}
	})

	t.Run("Colors=2", func(t *testing.T) {
		// Columns=2, Colors=2 → rowBytes=4, bpp=2
		rawStream := []byte{1, 2, 1, 2}
		param := filterMakeDict(map[string]any{
			"Predictor": int64(2),
			"Columns":   int64(2),
			"Colors":    int64(2),
		})
		rd, err := applyPredictor(bytes.NewReader(rawStream), param)
		if err != nil {
			t.Fatalf("applyPredictor TIFF Colors=2: %v", err)
		}
		got, err := io.ReadAll(rd)
		if err != nil {
			t.Fatalf("ReadAll: %v", err)
		}
		want := []byte{1, 2, 2, 4}
		if !bytes.Equal(got, want) {
			t.Fatalf("TIFF Colors=2: got %v, want %v", got, want)
		}
	})
}

// ---------------------------------------------------------------------------
// TestPredictorParamErrors — validation errors
// ---------------------------------------------------------------------------

func TestPredictorParamErrors(t *testing.T) {
	// Predictor 3 — unsupported
	t.Run("Predictor3", func(t *testing.T) {
		param := filterMakeDict(map[string]any{
			"Predictor": int64(3),
			"Columns":   int64(4),
		})
		_, err := applyPredictor(bytes.NewReader([]byte{0}), param)
		if err == nil {
			t.Fatal("expected error for unsupported predictor 3")
		}
	})

	// Columns = 0 — below minimum
	t.Run("Columns0", func(t *testing.T) {
		param := filterMakeDict(map[string]any{
			"Predictor": int64(12),
			"Columns":   int64(0),
		})
		_, err := applyPredictor(bytes.NewReader([]byte{0}), param)
		if err == nil {
			t.Fatal("expected error for Columns=0")
		}
	})

	// Columns > maxPNGColumns — above maximum
	t.Run("ColumnsOverMax", func(t *testing.T) {
		param := filterMakeDict(map[string]any{
			"Predictor": int64(12),
			"Columns":   int64(maxPNGColumns + 1),
		})
		_, err := applyPredictor(bytes.NewReader([]byte{0}), param)
		if err == nil {
			t.Fatalf("expected error for Columns=%d", maxPNGColumns+1)
		}
	})

	// Colors = 0 — below minimum
	t.Run("Colors0", func(t *testing.T) {
		param := filterMakeDict(map[string]any{
			"Predictor": int64(12),
			"Columns":   int64(4),
			"Colors":    int64(0),
		})
		_, err := applyPredictor(bytes.NewReader([]byte{0}), param)
		if err == nil {
			t.Fatal("expected error for Colors=0")
		}
	})

	// BitsPerComponent = 3 — not in {1,2,4,8,16}
	t.Run("BPC3", func(t *testing.T) {
		param := filterMakeDict(map[string]any{
			"Predictor":        int64(12),
			"Columns":          int64(4),
			"BitsPerComponent": int64(3),
		})
		_, err := applyPredictor(bytes.NewReader([]byte{0}), param)
		if err == nil {
			t.Fatal("expected error for BitsPerComponent=3")
		}
	})
}

// ---------------------------------------------------------------------------
// TestPredictorPNGUnknownFilterType — unknown row filter type → error
// ---------------------------------------------------------------------------

func TestPredictorPNGUnknownFilterType(t *testing.T) {
	// filter byte = 5 is not a valid PNG filter type
	rawStream := []byte{5, 10, 20, 30}
	param := filterMakeDict(map[string]any{
		"Predictor": int64(12),
		"Columns":   int64(3),
	})
	rd, err := applyPredictor(bytes.NewReader(rawStream), param)
	if err != nil {
		t.Fatalf("applyPredictor: %v", err)
	}
	buf := make([]byte, 64)
	_, err = rd.Read(buf)
	if err == nil {
		t.Fatal("expected error for unknown PNG row filter type, got nil")
	}
}
