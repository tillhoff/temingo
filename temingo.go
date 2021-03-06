package main

import (
	"bytes"
	"html/template"
	"io/ioutil"
	"log"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Masterminds/sprig"
	"github.com/PuerkitoBio/purell"
	"github.com/imdario/mergo"
	"github.com/otiai10/copy"
	"github.com/radovskyb/watcher"
	gitignore "github.com/sabhiram/go-gitignore"
	flag "github.com/spf13/pflag"
	"gopkg.in/yaml.v3"
)

var (
	debug bool
	watch bool

	valuesFilePaths         []string
	inputDir                string
	partialsDir             string
	outputDir               string
	staticDir               string
	templateExtension       string
	singleTemplateExtension string
	partialExtension        string
	temingoignoreFilePath   string

	listListObjects = make(map[string]map[string]interface{})

	pathValidator = "^[a-z0-9-_./]+$"
	rexp          = regexp.MustCompile(pathValidator)
)

type Breadcrumb struct {
	Name, Path interface{}
}

func createFolderIfNotExists(path string) {
	os.MkdirAll(path, os.ModePerm)
}

func createBreadcrumbs(path string) []Breadcrumb {
	if debug {
		log.Println("Creating breadcrumbs for '" + path + "'.")
	}
	breadcrumbs := []Breadcrumb{}
	currentPath := ""
	dirNames := strings.Split(path, "/")
	for ok := true; ok; ok = (len(dirNames) > 1) { // last one is not considered, so no self-reference occurs
		currentPath = currentPath + "/" + dirNames[0]
		breadcrumb := Breadcrumb{dirNames[0], currentPath}
		breadcrumbs = append(breadcrumbs, breadcrumb)
		dirNames = dirNames[1:] // remove first one, as it is now added to 'currentPath'
	}

	return breadcrumbs
}

func isExcludedByTemingoignore(srcPath string, additionalExclusions []string) bool {
	srcPath = "/" + srcPath

	ignore, err := gitignore.CompileIgnoreFileAndLines(temingoignoreFilePath, additionalExclusions...)
	if err != nil {
		log.Fatalln(err)
	}

	if ignore.MatchesPath(srcPath) {
		if debug {
			log.Println("Exclusion triggered at '" + srcPath + "', specified in '" + temingoignoreFilePath + "'.")
		}
		return true
	}

	return false
}

func isExcluded(srcPath string, additionalExclusions []string) bool {
	srcPath = "/" + srcPath

	additionalExclusions = append(additionalExclusions, "/"+temingoignoreFilePath)      // always ignore the ignore file itself
	additionalExclusions = append(additionalExclusions, "/"+path.Join(outputDir, "**")) // always ignore the outputDir
	additionalExclusions = append(additionalExclusions, "/"+path.Join(staticDir, "**")) // always ignore the staticDir

	ignore, err := gitignore.CompileIgnoreFileAndLines(temingoignoreFilePath, additionalExclusions...)
	if err != nil {
		log.Fatalln(err)
	}

	if ignore.MatchesPath((srcPath)) {
		if debug {
			log.Println("Exclusion triggered at '" + srcPath + "', specified internally.")
		}
		return true
	}

	return false
}

func getTemplates(fromPath string, extension string, additionalExclusions []string) [][]string {
	var templates [][]string

	dirContents, err := ioutil.ReadDir(fromPath)
	if err != nil {
		log.Fatalln(err)
	}
	for _, entry := range dirContents {
		if !(entry.Name()[:1] == ".") { // ignore hidden files/folders
			entryPath := path.Join(fromPath, entry.Name())
			if fromPath == "." { // path.Join adds this to the filename directly ... which has to be prevented here
				entryPath = entry.Name()
			}
			if !isExcluded(entryPath, additionalExclusions) { // Make all paths absolute from working-directory
				if entry.IsDir() {
					templates = append(templates, getTemplates(entryPath, extension, additionalExclusions)...)
				} else if strings.HasSuffix(entry.Name(), extension) {
					if !rexp.MatchString(entryPath) {
						log.Fatalln("The path '" + entryPath + "' doesn't validate against the regular expression '" + pathValidator + "'.")
					}
					fileContent, err := ioutil.ReadFile(entryPath)
					if err != nil {
						log.Fatalln(err)
					}
					templates = append(templates, []string{entryPath, string(fileContent)})
				}
			}
		}
	}

	return templates
}

func parseTemplateFiles(name string, baseTemplate string, partialTemplates [][]string) *template.Template {
	tpl := template.New(name)

	funcMap := sprig.HtmlFuncMap()

	extrafuncMap := template.FuncMap{
		"addPercentage": func(a string, b string) string {
			aInt, err := strconv.Atoi(a[:len(a)-1])
			if err != nil {
				log.Fatalln(err)
			}
			bInt, err := strconv.Atoi(b[:len(b)-1])
			if err != nil {
				log.Fatalln(err)
			}
			cInt := aInt + bInt
			return strconv.Itoa(cInt) + "%"
		},
		"include": func(name string, data map[string]interface{}) string {
			var buf strings.Builder
			err := tpl.ExecuteTemplate(&buf, name, data)
			if err != nil {
				log.Fatalln(err)
			}
			result := buf.String()
			return result
		},
		"safeHTML": func(s string) template.HTML {
			return template.HTML(s)
		},
		"safeCSS": func(s string) template.CSS {
			return template.CSS(s)
		},
		"list": func(listPaths ...string) map[string]interface{} {
			listObjects := make(map[string]interface{})
			if len(listPaths) == 0 { // If no path is provided
				listPaths = append(listPaths, filepath.Dir(name)) // Add the default path (folder containing the template)
			}
			for _, listPath := range listPaths {
				mergo.Merge(&listObjects, loadListObjects(listPath))
				listListObjects[listPath] = listObjects
			}
			return listObjects
		},
		"urlize": func(oldContent string) string {
			newContent, err := purell.NormalizeURLString(strings.ReplaceAll(oldContent, " ", "_"), purell.FlagsSafe)
			if err != nil {
				log.Fatalln(err)
			}
			newContent = strings.ToLower(newContent) // Also convert everything to lowercase. Arguable.
			if debug {
				log.Println("Urlized '" + oldContent + "' to '" + newContent + "'.")
			}
			return newContent
		},
		"capitalize": func(oldContent string) string {
			newContent := strings.Title(oldContent)
			if debug {
				log.Println("Capitalized '" + oldContent + "' to '" + newContent + "'.")
			}
			return newContent
		},
	}
	for k, v := range extrafuncMap {
		funcMap[k] = v
	}

	for index := range partialTemplates {
		partialTemplateContent := partialTemplates[index][1]
		_, err := tpl.Funcs(funcMap).Parse(partialTemplateContent)
		if err != nil {
			log.Fatalln(err)
		}
	}
	_, err := tpl.Funcs(funcMap).Parse(baseTemplate)
	if err != nil {
		log.Fatalln(err)
	}
	return tpl
}

func writeTemplateToFile(filePath string, content []byte) error {
	dirPath := strings.TrimSuffix(filePath, path.Base(filePath))
	createFolderIfNotExists(dirPath)
	err := ioutil.WriteFile(filePath, content, os.ModePerm)
	return err
}

func readCliFlags() {
	var (
		info os.FileInfo
		err  error
	)

	flag.StringSliceVarP(&valuesFilePaths, "valuesfile", "f", []string{"values.yaml"}, "Sets the path(s) to the values-file(s).")
	flag.StringVarP(&inputDir, "inputDir", "i", ".", "Sets the path to the template-file-directory.")
	flag.StringVarP(&partialsDir, "partialsDir", "p", "partials", "Sets the path to the partials-directory.")
	flag.StringVarP(&outputDir, "outputDir", "o", "output", "Sets the destination-path for the compiled templates.")
	flag.StringVarP(&staticDir, "staticDir", "s", "static", "Sets the source-path for the static files.")
	flag.StringVarP(&templateExtension, "templateExtension", "t", ".template", "Sets the extension of the template files.")
	flag.StringVar(&singleTemplateExtension, "singleTemplateExtension", ".single.template", "Sets the extension of the single-view template files. Automatically excluded from normally loaded templates.")
	flag.StringVar(&partialExtension, "partialExtension", ".partial", "Sets the extension of the partial files.") //TODO: not necessary, should be the same as templateExtension, since they are already distringuished by directory -> Might be useful when "modularization" will be implemented
	flag.StringVar(&temingoignoreFilePath, "temingoignore", ".temingoignore", "Sets the path to the ignore file.")
	flag.BoolVarP(&watch, "watch", "w", false, "Watches the template-file-directory, partials-directory and values-files.")
	flag.BoolVarP(&debug, "debug", "d", false, "Enables the debug mode.")

	flag.Parse() // Actually read the configured cli-flags

	for i, valuesfilePath := range valuesFilePaths { // for each path stated
		valuesFilePaths[i] = path.Clean(valuesfilePath) // clean path
		info, err = os.Stat(valuesFilePaths[i])
		if os.IsNotExist(err) { // if path doesn't exist
			log.Fatalln("Values file does not exist: " + valuesFilePaths[i])
		} else if info.IsDir() { // if is not a directoy
			log.Fatalln("Values file is not a file (but a directory): " + valuesFilePaths[i])
		}
	}

	inputDir = path.Clean(inputDir)
	info, err = os.Stat(inputDir)
	if os.IsNotExist(err) { // if path doesn't exist
		log.Fatalln("Given input-directory does not exist: " + inputDir)
	} else if !info.IsDir() { // if is not a directory
		log.Fatalln("Given input-directory is not a directory: " + inputDir)
	}

	partialsDir = path.Clean(partialsDir)
	info, err = os.Stat(partialsDir)
	if os.IsNotExist(err) { // if path doesn't exist
		log.Fatalln("Given partial-files-directory does not exist: " + partialsDir)
	} else if !info.IsDir() { // if is not a directory
		log.Fatalln("Given partial-files-directory is not a directory: " + partialsDir)
	}

	outputDir = path.Clean(outputDir)
	info, err = os.Stat(outputDir)
	if os.IsNotExist(err) { // if path doesn't exist
		log.Fatalln("Given output-directory does not exist: " + outputDir)
	} else if !info.IsDir() { // if is not a directory
		log.Fatalln("Given output-directory is not a directory: " + outputDir)
	}

	staticDir = path.Clean(staticDir)
	info, err = os.Stat(staticDir)
	if os.IsNotExist(err) { // if path doesn't exist
		log.Fatalln("Given static-files-directory does not exist: " + staticDir)
	} else if !info.IsDir() { // if is not a directory
		log.Fatalln("Given static-files-directory is not a directory: " + staticDir)
	}
}

func getMappedValues() map[string]interface{} {
	var mappedValues map[string]interface{}
	for _, v := range valuesFilePaths {
		tempMappedValues := loadYaml(v)

		err := mergo.Merge(&mappedValues, tempMappedValues, mergo.WithOverride)
		if err != nil {
			log.Fatalln(err)
		}
	}
	return mappedValues
}

func runTemplate(mappedValues map[string]interface{}, templateName string, template string, partialTemplates [][]string, outputFilePath string) {
	outputBuffer := new(bytes.Buffer)
	outputBuffer.Reset()
	tpl := parseTemplateFiles(templateName, template, partialTemplates)
	mappedValues["breadcrumbs"] = createBreadcrumbs(filepath.Dir(templateName))
	err := tpl.Execute(outputBuffer, mappedValues)
	if err != nil {
		log.Fatalln(err)
	}
	if _, err := os.Stat(outputDir); os.IsNotExist(err) { // If output directory doesn't exist
		createFolderIfNotExists(outputDir)
	}
	err = writeTemplateToFile(outputFilePath, outputBuffer.Bytes())
	if err != nil {
		log.Fatalln(err)
	}
}

func render() {
	// #####
	// START reading value files
	// #####
	if debug {
		log.Println("*** Reading values file(s) ... ***")
	}
	mappedValues := getMappedValues()
	if debug {
		valuesYaml, err := yaml.Marshal(mappedValues)
		if err != nil {
			log.Fatalln(err)
		}
		log.Println("*** General values-object: ***\n" + string(valuesYaml))
	}

	// #####
	// END reading value files
	// START normal templating
	// #####

	templates := getTemplates(inputDir, templateExtension, []string{"**/*" + singleTemplateExtension}) // get full html templates - with names
	partialTemplates := getTemplates(partialsDir, partialExtension, []string{})                        // get partial html templates - without names

	for _, template := range templates {
		outputFilePath := path.Join(outputDir, strings.TrimSuffix(template[0], templateExtension))
		if debug {
			log.Println("Writing output file '" + outputFilePath + "' ...")
		}
		runTemplate(mappedValues, template[0], template[1], partialTemplates, outputFilePath)
	}

	// #####
	// END normal templating
	// START single-view templating
	// #####

	// identify & collect single-view templates via their extension
	singleTemplates := getTemplates(inputDir, singleTemplateExtension, []string{
		path.Join(inputDir, partialsDir, "**"),
		path.Join(inputDir, outputDir, "**"),
	}) // get full html templates - with names

	// for each of the single-view templates
	for _, template := range singleTemplates {
		templateName := template[0]
		template := template[1]
		// search all configurations

		dirContents, err := ioutil.ReadDir(filepath.Dir(templateName))
		if err != nil {
			log.Fatalln(err)
		}

		itemValues := make(map[string]interface{})

		// Read item-specific values, so they are available independent of the items way of the configuration
		for _, dirEntry := range dirContents {
			if dirEntry.IsDir() {
				if _, err := os.Stat(path.Join(filepath.Dir(templateName), dirEntry.Name(), "index.yaml")); err == nil { // if the dirEntry-folder contains an "index.yaml"
					itemValues[path.Join(filepath.Dir(templateName), dirEntry.Name())] = loadYaml(path.Join(filepath.Dir(templateName), dirEntry.Name(), "index.yaml"))
				}
			}
		}

		for itemPath, itemValue := range itemValues {
			// load corresponding additional values into mappedValues["Item"]
			extendedMappedValues := mappedValues
			itemPath = strings.TrimSuffix(itemPath, filepath.Ext(itemPath))
			fileName := strings.TrimSuffix(filepath.Base(templateName), singleTemplateExtension)
			extendedMappedValues["ItemPath"] = "/" + itemPath
			extendedMappedValues["Item"] = itemValue
			outputFilePath := path.Join(outputDir, itemPath, fileName)
			if debug {
				log.Println("Writing single-view output from '" + itemPath + "*' to '" + outputFilePath + "' ...") // itemPath is incomplete; either its a yaml-file or a folder containing an index.yaml -> Therefore it has the '*' behind it.
			}
			runTemplate(extendedMappedValues, templateName, template, partialTemplates, outputFilePath)
		}
	}

	// #####
	// END single-view templating
	// #####
}

func watchAll() {
	log.Println("*** Starting to watch for file changes ... ***")

	// ignoring before adding, so the "to-be-ignored" paths won't be added
	w := watcher.New()

	// SetMaxEvents to 1 to allow at most 1 event's to be received
	// on the Event channel per watching cycle.
	// If SetMaxEvents is not set, the default is to send all events.
	w.SetMaxEvents(1)

	w.Ignore(outputDir) // ignore the outputfolder

	w.Ignore(".git") // ignore the git-folder natively

	if err := w.AddRecursive(inputDir); err != nil { // watch the input-files-directory recursively
		log.Fatalln(err)
	}
	if err := w.AddRecursive(partialsDir); err != nil { // watch the partials-files-directory recursively
		log.Fatalln(err)
	}
	for _, valuesFile := range valuesFilePaths { // for each valuesfilepath
		if err := w.Add(valuesFile); err != nil { // watch the values-file
			log.Fatalln(err)
		}
	}

	if debug {
		log.Println("Watched paths/files:")
		// Print a list of all of the files and folders currently being watched and their paths.
		for watchedPath, f := range w.WatchedFiles() {
			log.Println(path.Join(watchedPath, f.Name()))
		}
	}

	go func() {
		for { // while true
			select {
			case event := <-w.Event: // receive events
				log.Println("*** Rebuilding because of a change in", event.Path, "***")
				rebuildOutput()
			case err := <-w.Error: // receive errors
				log.Fatalln(err)
			case <-w.Closed:
				return
			}
		}
	}()

	// Start the watching process - it'll check for changes every 100ms.
	if err := w.Start(time.Millisecond * 100); err != nil {
		log.Fatalln(err)
	}
}

func rebuildOutput() {
	// #####
	// START Delete output-dir contents
	// #####

	if debug {
		log.Println("*** Deleting contents in output-dir ... ***")
	}

	dirContents, err := ioutil.ReadDir(outputDir)
	if err != nil {
		log.Fatalln(err)
	}
	for _, element := range dirContents {
		elementPath := path.Join(outputDir, element.Name())
		if debug {
			log.Println("Deleting output-dir content at: " + elementPath)
		}
		err = os.RemoveAll(elementPath)
		if err != nil {
			log.Fatalln(err)
		}
	}

	// #####
	// END Delete output-dir contents
	// START Copy static-dir contents to output-dir
	// #####

	if debug {
		log.Println("*** Copying contents of static-dir to output-dir ... ***")
	}

	err = copy.Copy(staticDir, outputDir)
	if err != nil {
		log.Fatalln(err)
	}

	// #####
	// END Copy static-dir-contents to output-dir
	// START Copy other contents to output-dir
	// #####

	if debug {
		log.Println("*** Copying other contents to output-dir ... ***")
	}

	opt := copy.Options{
		Skip: func(src string) (bool, error) {
			skip := false
			if isExcluded(src, []string{path.Join("/", partialsDir), "**/*" + templateExtension, "**/index.yaml"}) || isExcludedByTemingoignore(src, []string{}) {
				skip = true
			}
			return skip, nil
		},
	}
	err = copy.Copy(inputDir, outputDir, opt)
	if err != nil {
		log.Fatalln(err)
	}

	// #####
	// END Copy other contents to output-dir
	// START Render templates
	// #####

	if debug {
		log.Println("*** Starting templating process ... ***")
	}

	render()
	log.Println("*** Successfully built contents. ***")

	// #####
	// END Render templates
	// #####
}

func loadYaml(filePath string) map[string]interface{} {
	var mappedObject map[string]interface{}
	values, err := ioutil.ReadFile(filePath)
	if err != nil {
		log.Fatalln(err)
	}
	yaml.Unmarshal([]byte(values), &mappedObject) // store yaml into map

	// valuesYaml, err := yaml.Marshal(mappedValues) // convert map to yaml/string
	return mappedObject
}

func loadListObjects(listPath string) map[string]interface{} {
	if debug {
		log.Println("*** Loading list objects from '" + listPath + "' ... ***")
	}
	contents, err := ioutil.ReadDir(path.Join(path.Clean("."), path.Clean(listPath)))
	if err != nil {
		log.Fatalln(err)
	}
	mappedObjects := make(map[string]interface{})
	for _, element := range contents {
		elementPath := path.Join(listPath, element.Name()) // f.e. list/element1 for folders
		indexPath := path.Join(elementPath, "index.yaml")  // f.e. list/element1/index.yaml
		if _, err := os.Stat(indexPath); err == nil {      // if list/element1/index.yaml exists
			if !rexp.MatchString(indexPath) { // if path is not good for urls
				log.Fatalln("The path '" + indexPath + "' for the list object must validate against the regular expression '" + pathValidator + "'.")
			}
			tempMappedObject := loadYaml(indexPath)      // f.e. list/element1/index.yaml
			tempMappedObject["Path"] = "/" + elementPath // will become /[.../]list/element1 (or actually /[.../]list/element1/index.html)
			mappedObjects[elementPath] = tempMappedObject
			if debug {
				log.Println("Loaded object from '" + indexPath + "' ...")
			}
		}
	}

	return mappedObjects
}

func main() {
	// #####
	// START declaring variables
	// #####

	// no log.Println for debug before this, because the flags have to be read first ;)
	readCliFlags()
	// # example $> ./template -valuesfile values.yaml -inputDir ./ -partialsDir partials-html/ -templateExtension .html.template -generatedExtension .html

	if debug {
		log.Println("valuesFilePaths:", valuesFilePaths)
		log.Println("inputDir:", inputDir)
		log.Println("partialsDir:", partialsDir)
		log.Println("outputDir:", outputDir)
		log.Println("templateExtension:", templateExtension)
		log.Println("singleTemplateExtension:", singleTemplateExtension)
		log.Println("partialExtension:", partialExtension)
		log.Println("temingoignoreFilePath:", temingoignoreFilePath)
		log.Println("staticDir:", staticDir)
		log.Println("watch:", watch)
	}

	// #####
	// END declaring variables
	// START rendering
	// #####

	if !watch { // if not watching
		rebuildOutput() // delete old contents of output-folder & copy static contents & render templates once
	} else { // else (== if watching)
		watchAll() // start to watch
	}

	// #####
	// END rendering
	// #####
}
