package main

import (
	"strings"

	"github.com/spf13/cobra"
)

func contextsCompletionFunc(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	rawConfig, err := kubeconfigArgs.ToRawKubeConfigLoader().RawConfig()
	if err != nil {
		return completionError(err)
	}

	var comps []string

	for name := range rawConfig.Contexts {
		if strings.HasPrefix(name, toComplete) {
			comps = append(comps, name)
		}
	}

	return comps, cobra.ShellCompDirectiveNoFileComp
}

func completionError(err error) ([]string, cobra.ShellCompDirective) {
	cobra.CompError(err.Error())
	return nil, cobra.ShellCompDirectiveError
}
