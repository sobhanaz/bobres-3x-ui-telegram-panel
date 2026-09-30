package license

import "encoding/json"

// jsonMarshal is a test helper alias kept in its own file so license.go stays free of test-only code.
func jsonMarshal(v any) ([]byte, error) { return json.Marshal(v) }
