package fence_test

import (
	"context"
	"fmt"
	"log"

	"github.com/sonnes/gopi/pkg/sandbox/fence"
)

// Run the tests of a project inside the Workspace policy, with the
// network on so that the build can download modules.
func Example() {
	opts := append(fence.Workspace("."), fence.Network(true))
	f, err := fence.New(opts...)
	if err != nil {
		log.Fatal(err)
	}

	cmd, err := f.Command(context.Background(), ".", "go", "test", "./...")
	if err != nil {
		log.Fatal(err)
	}
	out, err := cmd.CombinedOutput()
	fmt.Printf("%s", out)
	if err != nil {
		log.Fatal(err)
	}
}
