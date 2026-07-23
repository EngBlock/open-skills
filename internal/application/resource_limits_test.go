package application

import (
	"errors"
	"testing"
)

func TestDefaultRemoteResourceLimits(t *testing.T) {
	limits := defaultResourceLimits()
	if limits.MaxFileBytes != 10<<20 || limits.MaxTotalBytes != 100<<20 || limits.MaxFiles != 5_000 || limits.MaxDepth != 20 {
		t.Fatalf("default resource limits = %#v", limits)
	}
}

func TestDefaultResourceLimitBoundaries(t *testing.T) {
	limits := defaultResourceLimits()
	for _, test := range []struct {
		name      string
		size      int64
		wantError bool
	}{
		{"file immediately below", limits.MaxFileBytes - 1, false},
		{"file exactly at", limits.MaxFileBytes, false},
		{"file above", limits.MaxFileBytes + 1, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := newResourceBudget(limits).addFile("payload", test.size)
			if test.wantError != isResourceLimitError(err) {
				t.Fatalf("error = %v, want resource error %v", err, test.wantError)
			}
		})
	}

	for _, test := range []struct {
		name      string
		lastSize  int64
		extraByte bool
		wantError bool
	}{
		{"total immediately below", limits.MaxFileBytes - 1, false, false},
		{"total exactly at", limits.MaxFileBytes, false, false},
		{"total above", limits.MaxFileBytes, true, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			budget := newResourceBudget(limits)
			var err error
			for index := 0; index < 10; index++ {
				size := limits.MaxFileBytes
				if index == 9 {
					size = test.lastSize
				}
				if err = budget.addFile("payload", size); err != nil {
					break
				}
			}
			if err == nil && test.extraByte {
				err = budget.addFile("extra", 1)
			}
			if test.wantError != isResourceLimitError(err) {
				t.Fatalf("error = %v, want resource error %v", err, test.wantError)
			}
		})
	}

	for _, test := range []struct {
		name      string
		files     int
		wantError bool
	}{
		{"count immediately below", limits.MaxFiles - 1, false},
		{"count exactly at", limits.MaxFiles, false},
		{"count above", limits.MaxFiles + 1, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			budget := newResourceBudget(limits)
			var err error
			for index := 0; index < test.files; index++ {
				if err = budget.addFile("payload", 0); err != nil {
					break
				}
			}
			if test.wantError != isResourceLimitError(err) {
				t.Fatalf("error = %v, want resource error %v", err, test.wantError)
			}
		})
	}

	for _, test := range []struct {
		name      string
		depth     int
		wantError bool
	}{
		{"depth immediately below", limits.MaxDepth - 1, false},
		{"depth exactly at", limits.MaxDepth, false},
		{"depth above", limits.MaxDepth + 1, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := limits.checkDepth("nested", test.depth)
			if test.wantError != isResourceLimitError(err) {
				t.Fatalf("error = %v, want resource error %v", err, test.wantError)
			}
		})
	}
}

func TestResourceLimitBoundaries(t *testing.T) {
	tests := []struct {
		name   string
		limits resourceLimits
		apply  func(*resourceBudget) error
	}{
		{
			name:   "file size immediately below",
			limits: resourceLimits{MaxFileBytes: 10, MaxTotalBytes: 100, MaxFiles: 10, MaxDepth: 10},
			apply:  func(budget *resourceBudget) error { return budget.addFile("payload", 9) },
		},
		{
			name:   "file size exactly at",
			limits: resourceLimits{MaxFileBytes: 10, MaxTotalBytes: 100, MaxFiles: 10, MaxDepth: 10},
			apply:  func(budget *resourceBudget) error { return budget.addFile("payload", 10) },
		},
		{
			name:   "file size above",
			limits: resourceLimits{MaxFileBytes: 10, MaxTotalBytes: 100, MaxFiles: 10, MaxDepth: 10},
			apply:  func(budget *resourceBudget) error { return budget.addFile("payload", 11) },
		},
		{
			name:   "total immediately below",
			limits: resourceLimits{MaxFileBytes: 100, MaxTotalBytes: 10, MaxFiles: 10, MaxDepth: 10},
			apply: func(budget *resourceBudget) error {
				if err := budget.addFile("first", 4); err != nil {
					return err
				}
				return budget.addFile("second", 5)
			},
		},
		{
			name:   "total exactly at",
			limits: resourceLimits{MaxFileBytes: 100, MaxTotalBytes: 10, MaxFiles: 10, MaxDepth: 10},
			apply: func(budget *resourceBudget) error {
				if err := budget.addFile("first", 4); err != nil {
					return err
				}
				return budget.addFile("second", 6)
			},
		},
		{
			name:   "total above",
			limits: resourceLimits{MaxFileBytes: 100, MaxTotalBytes: 10, MaxFiles: 10, MaxDepth: 10},
			apply: func(budget *resourceBudget) error {
				if err := budget.addFile("first", 4); err != nil {
					return err
				}
				return budget.addFile("second", 7)
			},
		},
		{
			name:   "file count immediately below",
			limits: resourceLimits{MaxFileBytes: 100, MaxTotalBytes: 100, MaxFiles: 3, MaxDepth: 10},
			apply: func(budget *resourceBudget) error {
				if err := budget.addFile("first", 1); err != nil {
					return err
				}
				return budget.addFile("second", 1)
			},
		},
		{
			name:   "file count exactly at",
			limits: resourceLimits{MaxFileBytes: 100, MaxTotalBytes: 100, MaxFiles: 3, MaxDepth: 10},
			apply: func(budget *resourceBudget) error {
				for _, name := range []string{"first", "second", "third"} {
					if err := budget.addFile(name, 1); err != nil {
						return err
					}
				}
				return nil
			},
		},
		{
			name:   "file count above",
			limits: resourceLimits{MaxFileBytes: 100, MaxTotalBytes: 100, MaxFiles: 3, MaxDepth: 10},
			apply: func(budget *resourceBudget) error {
				for _, name := range []string{"first", "second", "third", "fourth"} {
					if err := budget.addFile(name, 1); err != nil {
						return err
					}
				}
				return nil
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			budget := newResourceBudget(test.limits)
			err := test.apply(budget)
			wantError := test.name == "file size above" || test.name == "total above" || test.name == "file count above"
			if wantError && !isResourceLimitError(err) {
				t.Fatalf("error = %v; want resource limit error", err)
			}
			if !wantError && err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestTraversalDepthBoundaries(t *testing.T) {
	limits := resourceLimits{MaxFileBytes: 100, MaxTotalBytes: 100, MaxFiles: 10, MaxDepth: 5}
	for _, test := range []struct {
		name      string
		depth     int
		wantError bool
	}{
		{"immediately below", 4, false},
		{"exactly at", 5, false},
		{"above", 6, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := limits.checkDepth("a/b", test.depth)
			if test.wantError && !isResourceLimitError(err) {
				t.Fatalf("error = %v; want resource limit error", err)
			}
			if !test.wantError && err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestParseResourceLimitOverrides(t *testing.T) {
	limits := defaultResourceLimits()
	arguments := []string{"--max-file-bytes", "11534336", "--max-total-bytes", "2147483648", "--max-files", "6000", "--max-depth", "25"}
	for index := 0; index < len(arguments); index++ {
		matched, next, err := parseResourceLimitOption(arguments, index, &limits)
		if err != nil {
			t.Fatal(err)
		}
		if !matched {
			t.Fatalf("option %q was not matched", arguments[index])
		}
		index = next
	}
	if limits.MaxFileBytes != 11<<20 || limits.MaxTotalBytes != 2<<30 || limits.MaxFiles != 6000 || limits.MaxDepth != 25 {
		t.Fatalf("parsed limits = %#v", limits)
	}

	for _, arguments := range [][]string{{"--max-files"}, {"--max-depth", "0"}, {"--max-file-bytes", "ten"}, {"--max-total-bytes", "-1"}, {"--max-file-bytes", "9223372036854775807"}, {"--max-total-bytes", "9223372036854775807"}} {
		limits := defaultResourceLimits()
		matched, _, err := parseResourceLimitOption(arguments, 0, &limits)
		if !matched || err == nil {
			t.Fatalf("parseResourceLimitOption(%#v) = matched %v, error %v", arguments, matched, err)
		}
	}

	repeated := []string{"--max-files", "6", "--max-files", "7"}
	limits = defaultResourceLimits()
	if _, next, err := parseResourceLimitOption(repeated, 0, &limits); err != nil {
		t.Fatal(err)
	} else if matched, _, err := parseResourceLimitOption(repeated, next+1, &limits); !matched || err == nil {
		t.Fatalf("repeated option = matched %v, error %v", matched, err)
	}

	var limitErr *resourceLimitError
	if !errors.As(newResourceBudget(resourceLimits{MaxFileBytes: 1, MaxTotalBytes: 1, MaxFiles: 1, MaxDepth: 1}).addFile("too-large", 2), &limitErr) {
		t.Fatal("resource limit errors are not distinguishable")
	}
}
