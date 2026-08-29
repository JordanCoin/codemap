package main

import (
	"context"
	"fmt"
	"io"

	"codemap/internal/projectpath"
	"codemap/topology"
)

var buildTopologyGraphOnly = topology.BuildGraph

func canonicalTopologyRoot(root string) (string, error) {
	selection, err := projectpath.Select(root)
	if err != nil {
		return "", err
	}
	return selection.ProjectRoot, nil
}

func runTopologyMode(ctx context.Context, root, module, file, ecosystem string, jsonMode bool, output io.Writer) error {
	graph, _, err := buildTopologyGraphOnly(ctx, root)
	if err != nil {
		return err
	}
	graph = topology.FilterGraph(graph, ecosystem)
	if module != "" || file != "" {
		text, err := topology.FormatModuleContext(graph, module, file)
		if err != nil {
			return err
		}
		_, err = fmt.Fprint(output, text)
		return err
	}
	if jsonMode {
		data, err := topology.FormatGraphJSON(graph, "")
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(output, string(data))
		return err
	}
	_, err = fmt.Fprint(output, topology.FormatGraph(graph, ""))
	return err
}
