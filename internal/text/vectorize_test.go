package text

import (
	"reflect"
	"testing"
)

func TestStem(t *testing.T) {
	tests := []struct{ in, want string }{
		{"logs", "log"},
		{"tags", "tag"},
		{"buckets", "bucket"},
		{"modules", "module"},
		{"policies", "policy"},
		{"statuses", "status"},
		{"boxes", "box"},

		// The names that must survive intact. Terraform is full of three-letter
		// s-terminated identifiers, and folding them produces nonsense that then fails to
		// match the very thing it was meant to find.
		{"aws", "aws"},
		{"dns", "dns"},
		{"ips", "ips"},
		{"access", "access"},
		{"class", "class"},

		{"bucket", "bucket"},
		{"s3", "s3"},
	}

	for _, tc := range tests {
		if got := stem(tc.in); got != tc.want {
			t.Errorf("stem(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestTokenizeSplitsCompounds(t *testing.T) {
	// Almost every meaningful name in Terraform is a compound, so a whitespace tokenizer
	// would make aws_s3_bucket reachable only by typing it exactly.
	got := Tokenize("aws_s3_bucket.logs")
	want := []string{"aws", "s3", "bucket", "log"}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("Tokenize = %v, want %v", got, want)
	}
}

func TestTokenizeDropsStopWords(t *testing.T) {
	got := Tokenize("where does the audit log bucket live")
	for _, tok := range got {
		if stopWords[tok] {
			t.Errorf("stop word %q survived tokenization: %v", tok, got)
		}
	}
	if len(got) == 0 {
		t.Fatal("question reduced to nothing")
	}
}

func TestVectorizeFusedBridgesCompounds(t *testing.T) {
	// "s3 bucket" written as two words must reach a name written as one.
	q := VectorizeFused("s3 bucket")
	if q["s3bucket"] == 0 {
		t.Errorf("fused pair s3bucket missing from %v", q)
	}
	if q["s3bucket"] >= q["s3"] {
		t.Error("a fused pair is weighted as heavily as a real term; it should be a bridge, not evidence")
	}
}

func TestSimilarityIsSymmetricAndBounded(t *testing.T) {
	a := "aws_s3_bucket logs"
	b2 := "logs bucket"

	s1, s2 := Similarity(a, b2), Similarity(b2, a)
	if s1 != s2 {
		t.Errorf("Similarity is not symmetric: %v vs %v", s1, s2)
	}
	if s1 < 0 || s1 > 1.0001 {
		t.Errorf("Similarity out of range: %v", s1)
	}
	if Similarity(a, "kubernetes helm chart") > s1 {
		t.Error("unrelated text scored higher than related text")
	}
}
