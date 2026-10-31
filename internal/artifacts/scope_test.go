package artifacts

import "testing"

func TestValidExecutionID(t *testing.T) {
	for _, id := range []string{"00000000000000000000000001", "01ARZ3NDEKTSV4RRFFQ69G5FAV", "7ZZZZZZZZZZZZZZZZZZZZZZZZZ"} {
		if !ValidExecutionID(id) {
			t.Errorf("canonical ULID rejected: %q", id)
		}
	}
	for _, id := range []string{"", "00000000000000000000000000", "../../outside", "22222222-2222-4222-8222-222222222222", "01arz3ndektsv4rrffq69g5fav", "8ZZZZZZZZZZZZZZZZZZZZZZZZZ", "01ARZ3NDEKTSV4RRFFQ69G5FAI", "01ARZ3NDEKTSV4RRFFQ69G5FAO", "01ARZ3NDEKTSV4RRFFQ69G5FAU", "01ARZ3NDEKTSV4RRFFQ69G5FAV/", "01ARZ3NDEKTSV4RRFFQ69G5FA"} {
		if ValidExecutionID(id) {
			t.Errorf("invalid execution ID accepted: %q", id)
		}
	}
}
