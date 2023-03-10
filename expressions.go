package main

import (
	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
	log "github.com/sirupsen/logrus"
	"github.com/urfave/cli/v2"
	"gopkg.in/yaml.v3"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const ExpressionPattern = "\\$\\{[\\w-.\"()]+}"
const SecretExpressionPattern = "\\$\\{secrets.getValue\\([^{}]+\\)}"

var ExpressionsMap = map[string]string{
	"infra.kubernetes.namespace": "<+infra.namespace>",
	"infra.kubernetes.infraId":   "<+INFRA_KEY>",
	"infra.helm.releaseName":     "<+infra.releaseName>",
	"infra.name":                 "<+infra.name>",

	// Env Expressions
	"env.name":            "<+env.name>",
	"env.description":     "<+env.description>",
	"env.environmentType": "<+env.type>",
	"env.uuid":            "<+env.identifier>",

	// Service Expressions
	"service.name":        "<+service.name>",
	"service.Name":        "<+service.name>",
	"Service.name":        "<+service.name>",
	"service.tag":         "<+service.tags>",
	"service.uuid":        "<+service.identifier>",
	"service.description": "<+service.description>",

	// Artifact Expressions
	"artifact.metadata.image":          "<+artifact.image>",
	"artifact.metadata.tag":            "<+artifact.tag>",
	"artifact.source.dockerconfig":     "<+artifact.imagePullSecret>",
	"artifact.metadata.fileName":       "<+artifact.fileName>",
	"artifact.metadata.format":         "<+artifact.repositoryFormat>",
	"artifact.metadata.getSHA()":       "<+artifact.metadata.SHA>",
	"artifact.metadata.groupId":        "<+artifact.groupId>",
	"artifact.metadata.package":        "<+artifact.metadata.package>",
	"artifact.metadata.region":         "<+artifact.metadata.region>",
	"artifact.metadata.repository":     "<+artifact.repository>",
	"artifact.metadata.repositoryName": "<+artifact.repositoryName>",
	"artifact.metadata.url":            "<+artifact.url>",
	"artifact.buildNo":                 "<+artifact.tag>",

	// Rollback Artifact Expressions
	"rollbackArtifact.metadata.image":          "<+rollbackArtifact.image>",
	"rollbackArtifact.metadata.tag":            "<+rollbackArtifact.tag>",
	"rollbackArtifact.source.dockerconfig":     "<+rollbackArtifact.imagePullSecret>",
	"rollbackArtifact.metadata.fileName":       "<+rollbackArtifact.fileName>",
	"rollbackArtifact.metadata.format":         "<+rollbackArtifact.repositoryFormat>",
	"rollbackArtifact.metadata.getSHA()":       "<+rollbackArtifact.metadata.SHA>",
	"rollbackArtifact.metadata.groupId":        "<+rollbackArtifact.groupId>",
	"rollbackArtifact.metadata.package":        "<+rollbackArtifact.metadata.package>",
	"rollbackArtifact.metadata.region":         "<+rollbackArtifact.metadata.region>",
	"rollbackArtifact.metadata.repository":     "<+rollbackArtifact.repository>",
	"rollbackArtifact.metadata.repositoryName": "<+rollbackArtifact.repositoryName>",
	"rollbackArtifact.metadata.url":            "<+rollbackArtifact.url>",
	"rollbackArtifact.buildNo":                 "<+rollbackArtifact.tag>",

	// Application Expressions
	"app.name":        "<+project.name>",
	"app.description": "<+project.description>",

	// Http Step
	"httpResponseCode": "<+httpResponseCode>",
	"httpResponseBody": "<+httpResponseBody>",
	"httpMethod":       "<+httpMethod>",
	"httpUrl":          "<+httpUrl>",
}

var DynamicExpressions = map[string]interface{}{
	"workflow.variables": func(key string) string {
		return "<+stage.variables.." + key + ">"
	},
	"pipeline.variables": func(key string) string {
		return "<+pipeline.variables." + key + ">"
	},
	"serviceVariable": func(key string) string {
		return "<+serviceVariables." + key + ">"
	},
	"serviceVariables": func(key string) string {
		return "<+serviceVariables." + key + ">"
	},
	"service.variables": func(key string) string {
		return "<+serviceVariables." + key + ">"
	},
	"environmentVariable": func(key string) string {
		return "<+env.variables." + key + ">"
	},
	"environmentVariables": func(key string) string {
		return "<+env.variables." + key + ">"
	},
	"secrets.getValue(": func(key string) string {
		return "<+secrets.getValue(\"" + getSecretKeyWithScope(key) + "\")>"
	},
	"app.defaults": func(key string) string {
		return "<+variable." + key + ">"
	},
}

func getSecretKeyWithScope(key string) string {
	camelCase := ToCamelCase(key)
	switch migrationReq.SecretScope {
	case Account:
		return Account + "." + camelCase
	case Org:
		return Org + "." + camelCase
	default:
		return camelCase
	}
}

func ReplaceCurrentGenExpressionsWithNextGen(*cli.Context) (err error) {
	loadYamlFromFile(migrationReq.CustomExpressionsFile)

	extensions := Split(migrationReq.FileExtensions, ",")
	for i, ext := range extensions {
		extensions[i] = "." + ext
	}

	foundExpressionsMap := make(map[string][]string)
	var allExpressions []string

	// Fetch all expressions per file
	err = filepath.Walk("./", func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && EndsWith(info.Name(), extensions) && info.Mode().Type() != os.ModeSymlink {
			content, err := ReadFile(path)
			if err != nil {
				return err
			}
			foundExpressions := Set(FindAllExpressions(content))
			if len(foundExpressions) > 0 {
				foundExpressionsMap[path] = foundExpressions
				allExpressions = Set(append(allExpressions, foundExpressions...))
			}
		}
		return nil
	})

	if err != nil {
		return err
	}

	if len(foundExpressionsMap) == 0 {
		log.Info("No files found containing Harness expressions!")
		return nil
	}

	// Render a table with summary of expressions found
	data := make(map[string]interface{})
	for path, expList := range foundExpressionsMap {
		data[path] = strings.Join(expList, ", ")
	}
	renderSupportedExpressionsTable(allExpressions)
	renderTable("Files containing expressions", data)

	if migrationReq.DryRun {
		log.Info("Dry run is set to true. Skipping expressions replacement for all files")
		return err
	}

	// We are going to do an actual replacement
	notReplacedMap := make(map[string][]string)
	for path, expList := range foundExpressionsMap {
		content, err := ReadFile(path)
		if err != nil {
			return err
		}
		str, notReplaced := ReplaceAllExpressions(content, expList)
		if len(notReplaced) > 0 {
			notReplacedMap[path] = notReplaced
		}
		err = WriteToFile(path, []byte(str))
		if err != nil {
			return err
		}
		log.Infof("Replaced expressions from %s", path)
	}
	data = make(map[string]interface{})
	for path, expList := range notReplacedMap {
		data[path] = strings.Join(expList, ", ")
	}
	if len(data) > 0 {
		renderTable("Expressions not replaced", data)
	}
	return
}

func FindAllExpressions(str string) []string {
	// Generic expressions
	r := regexp.MustCompile(ExpressionPattern)
	allExpressions := r.FindAllString(str, -1)

	// Secret expressions
	r = regexp.MustCompile(SecretExpressionPattern)
	allExpressions = append(allExpressions, r.FindAllString(str, -1)...)
	return allExpressions
}

func ReplaceAllExpressions(str string, expressions []string) (string, []string) {
	var notReplaced []string
	for _, exp := range expressions {
		temp := exp[2 : len(exp)-1]
		val, ok := ExpressionsMap[temp]
		if ok {
			str = strings.ReplaceAll(str, exp, val)
		} else if len(getDynamicExpressionKey(temp)) > 0 {
			newVal := getDynamicExpressionValue(temp)
			str = strings.ReplaceAll(str, exp, newVal)
		} else {
			notReplaced = append(notReplaced, exp)
		}
	}
	return str, notReplaced
}

func renderSupportedExpressionsTable(data []string) {
	rowConfigAutoMerge := table.RowConfig{AutoMerge: true}
	title := "Equivalent Expressions"
	if len(data) > 0 {
		var rows []table.Row
		for _, exp := range data {
			temp := exp[2 : len(exp)-1]
			val, ok := ExpressionsMap[temp]
			check := "Yes"
			if !ok {
				dynamic := getDynamicExpressionKey(temp)
				if len(dynamic) > 0 {
					val = getDynamicExpressionValue(temp)
				}
				if len(dynamic) == 0 {
					check = "No"
				}
			}
			rows = append(rows, table.Row{exp, check, val})
		}
		t := table.NewWriter()
		t.SetOutputMirror(os.Stdout)
		t.AppendHeader(table.Row{title, title, title}, rowConfigAutoMerge)
		t.AppendSeparator()
		t.AppendHeader(table.Row{"First Gen", "Supported?", "Next Gen"})
		t.AppendSeparator()
		t.AppendRows(rows)
		t.AppendSeparator()
		t.SetColumnConfigs([]table.ColumnConfig{
			{Number: 1, AlignHeader: text.AlignCenter},
		})
		t.SortBy([]table.SortBy{
			{Number: 1, Mode: table.Asc},
		})
		t.SetStyle(table.StyleLight)
		t.Render()
	}

}

func getDynamicExpressionValue(key string) string {
	k := getDynamicExpressionKey(key)
	var dynamic string
	if strings.HasSuffix(k, "(") {
		dynamic = strings.Replace(key, k, "", 1)
		if strings.HasSuffix(dynamic, ")") {
			dynamic = dynamic[0 : len(dynamic)-1]
		}
	} else {
		dynamic = strings.Replace(key, k+".", "", 1)
	}
	return DynamicExpressions[k].(func(string2 string) string)(dynamic)
}

func getDynamicExpressionKey(key string) string {
	for exp, _ := range DynamicExpressions {
		if strings.HasPrefix(key, exp) {
			return exp
		}
	}
	return ""
}

func loadYamlFromFile(filePath string) {
	filePath = strings.TrimSpace(filePath)
	if len(filePath) == 0 {
		return
	}
	yFile, err := os.ReadFile(filePath)
	if err != nil {
		log.Fatal(err)
	}
	data := make(map[string]string)
	err = yaml.Unmarshal(yFile, &data)
	if err != nil {
		log.Fatal(err)
	}
	for k, v := range data {
		ExpressionsMap[k] = v
	}
	log.Infof("Successfully loaded %d custom expressions from the file", len(data))
}
