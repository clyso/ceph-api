package parity

import (
	"fmt"
	"sort"
	"strings"

	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
)

// AssertGRPCMethodsRouted returns nil if every gRPC method registered
// in protoregistry.GlobalFiles (excluding those in serviceFullNames
// matching any prefix in excludeServicePrefixes) has a matching
// selector in s. Otherwise reports each missing method and where to
// add it.
//
// The generated *.pb.go packages register their FileDescriptors on
// import; the test binary imports them via api/gen/grpc/go so
// GlobalFiles is populated by the time TestMain runs.
func AssertGRPCMethodsRouted(s *RouteSet, excludeServicePrefixes []string) error {
	selectors := s.Selectors()
	var missing []string

	protoregistry.GlobalFiles.RangeFiles(func(fd protoreflect.FileDescriptor) bool {
		services := fd.Services()
		for i := 0; i < services.Len(); i++ {
			svc := services.Get(i)
			fullSvc := string(svc.FullName())
			if matchesAnyPrefix(fullSvc, excludeServicePrefixes) {
				continue
			}
			methods := svc.Methods()
			for j := 0; j < methods.Len(); j++ {
				m := methods.Get(j)
				selector := fullSvc + "." + string(m.Name())
				if _, ok := selectors[selector]; !ok {
					missing = append(missing, selector)
				}
			}
		}
		return true
	})

	if len(missing) == 0 {
		return nil
	}
	sort.Strings(missing)

	var b strings.Builder
	fmt.Fprintf(&b, "%d gRPC method(s) have no rule in api/http.yaml:\n", len(missing))
	for _, m := range missing {
		fmt.Fprintf(&b, "  - %s\n", m)
	}
	b.WriteString("add a `- selector: <method>` entry under http.rules in api/http.yaml with the HTTP method and path, then run `make proto`")
	return fmt.Errorf("%s", b.String())
}

func matchesAnyPrefix(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}
