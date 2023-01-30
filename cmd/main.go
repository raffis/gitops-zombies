package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path"
	"strconv"
	"strings"

	gitopszombiesv1 "github.com/raffis/gitops-zombies/pkg/apis/gitopszombies/v1"
	"github.com/raffis/gitops-zombies/pkg/detector"
	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/util/homedir"
	"k8s.io/klog/v2"
	k8sget "k8s.io/kubectl/pkg/cmd/get"
)

const (
	version = "0.0.0-dev"
	commit  = "none"
	date    = "unknown"
)

type args struct {
	excludeCluster *[]string
	fail           bool
	includeAll     bool
	labelSelector  string
	nostream       bool
	version        bool
}

const (
	statusOK = iota
	statusFail
	statusZombiesDetected

	statusAnnotation = "status"
)

func main() {
	flags := args{}

	rootCmd, err := parseCliArgs(&flags)
	if err != nil {
		fmt.Printf("%v", err)
	}

	err = rootCmd.Execute()
	if err != nil {
		fmt.Printf("%v", err)
	}

	os.Exit(toExitCode(rootCmd.Annotations[statusAnnotation]))
}

func toExitCode(codeStr string) int {
	code, err := strconv.Atoi(codeStr)
	if err != nil {
		return statusFail
	}

	return code
}

func parseCliArgs(flags *args) (*cobra.Command, error) {
	kubeconfigArgs := genericclioptions.NewConfigFlags(false)
	printFlags := k8sget.NewGetPrintFlags()
	cfgFile := path.Join(homedir.HomeDir(), ".gitops-zombies.yaml")

	rootCmd := &cobra.Command{
		Use:           "gitops-zombies",
		SilenceUsage:  true,
		SilenceErrors: true,
		Short:         "Find kubernetes resources which are not managed by GitOps",
		Long:          `Finds all kubernetes resources from all installed apis on a kubernetes cluste and evaluates whether they are managed by a flux kustomization or a helmrelease.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.Annotations = make(map[string]string)
			cmd.Annotations[statusAnnotation] = strconv.Itoa(statusFail)

			conf, err := loadConfig(cfgFile)
			if err != nil {
				return err
			}

			status, err := run(conf, kubeconfigArgs, *flags, printFlags)
			if err != nil {
				return err
			}

			cmd.Annotations[statusAnnotation] = strconv.Itoa(status)
			return nil
		},
	}

	apiServer := ""
	kubeconfigArgs.APIServer = &apiServer
	kubeconfigArgs.AddFlags(rootCmd.PersistentFlags())

	rest.SetDefaultWarningHandler(rest.NewWarningWriter(io.Discard, rest.WarningWriterOptions{}))
	set := &flag.FlagSet{}
	klog.InitFlags(set)
	rootCmd.PersistentFlags().AddGoFlagSet(set)

	err := rootCmd.RegisterFlagCompletionFunc("context", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return contextsCompletionFunc(kubeconfigArgs, toComplete)
	})
	if err != nil {
		return nil, err
	}

	rootCmd.Flags().StringVarP(&cfgFile, "config", "", cfgFile, "Config file")
	rootCmd.Flags().StringVarP(printFlags.OutputFormat, "output", "o", *printFlags.OutputFormat, fmt.Sprintf(`Output format. One of: (%s). See custom columns [https://kubernetes.io/docs/reference/kubectl/overview/#custom-columns], golang template [http://golang.org/pkg/text/template/#pkg-overview] and jsonpath template [https://kubernetes.io/docs/reference/kubectl/jsonpath/].`, strings.Join(printFlags.AllowedFormats(), ", ")))
	rootCmd.Flags().BoolVarP(&flags.version, "version", "", flags.version, "Print version and exit")
	rootCmd.Flags().BoolVarP(&flags.includeAll, "include-all", "a", flags.includeAll, "Includes resources which are considered dynamic resources")
	rootCmd.Flags().StringVarP(&flags.labelSelector, "selector", "l", flags.labelSelector, "Label selector (Is used for all apis)")
	rootCmd.Flags().BoolVarP(&flags.nostream, "no-stream", "", flags.nostream, "Display discovered resources at the end instead of live")
	rootCmd.Flags().BoolVarP(&flags.fail, "fail", "", flags.fail, "Exit with an exit code > 0 if zombies are detected")
	flags.excludeCluster = rootCmd.Flags().StringSliceP("exclude-cluster", "", nil, "Exclude cluster from zombie detection (default none)")

	rootCmd.DisableAutoGenTag = true
	rootCmd.SetOut(os.Stdout)
	return rootCmd, nil
}

func loadConfig(configPath string) (*gitopszombiesv1.Config, error) {
	if _, err := os.Stat(configPath); err != nil {
		klog.V(1).Infof("Can't find config file at %s", configPath)
		return &gitopszombiesv1.Config{}, nil
	}

	json, err := os.ReadFile(configPath)
	if err != nil {
		return nil, err
	}

	err = gitopszombiesv1.AddToScheme(scheme.Scheme)
	if err != nil {
		return nil, err
	}

	obj, err := runtime.Decode(scheme.Codecs.UniversalDeserializer(), json)
	if err != nil {
		return nil, err
	}

	var cfg gitopszombiesv1.Config
	switch o := obj.(type) {
	case *gitopszombiesv1.Config:
		cfg = *o
	default:
		err = errors.New("unsupported config")
		return nil, err
	}

	return &cfg, nil
}

func run(conf *gitopszombiesv1.Config, kubeconfigArgs *genericclioptions.ConfigFlags, flags args, printFlags *k8sget.PrintFlags) (int, error) {
	if flags.version {
		fmt.Printf(`{"version":"%s","sha":"%s","date":"%s"}`+"\n", version, commit, date)
		return statusOK, nil
	}

	// default processing
	detect, err := detector.New(conf, kubeconfigArgs, detector.Args{
		ExcludeCluster: flags.excludeCluster,
		Fail:           flags.fail,
		IncludeAll:     flags.includeAll,
		LabelSelector:  flags.labelSelector,
		Nostream:       flags.nostream,
		PrintFlags:     printFlags,
	})
	if err != nil {
		return statusFail, err
	}
	resourceCount, allZombies, err := detect.DetectZombies()
	if err != nil {
		return statusFail, err
	}

	if flags.nostream {
		err = detect.PrintZombies(allZombies)
		if err != nil {
			return statusFail, err
		}
	}

	var totalZombies int
	for _, zombies := range allZombies {
		totalZombies += len(zombies)
	}

	if flags.nostream && *printFlags.OutputFormat == "" {
		fmt.Printf("\nSummary: %d resources found, %d zombies detected\n", resourceCount, totalZombies)
	}

	if flags.fail && totalZombies > 0 {
		return statusZombiesDetected, nil
	}

	return statusOK, nil
}
