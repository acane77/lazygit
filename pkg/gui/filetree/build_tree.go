package filetree

import (
	"sort"
	"strings"

	"github.com/jesseduffield/lazygit/pkg/commands/models"
)

func BuildTreeFromFiles(
	files []*models.File,
	showRootItem bool,
	cmp func(a, b *Node[models.File]) int,
) *Node[models.File] {
	root := &Node[models.File]{}

	childrenMapsByNode := make(map[*Node[models.File]]map[string]*Node[models.File])

	var curr *Node[models.File]
	for _, file := range files {
		splitPath := SplitFileTreePath(file.Path, showRootItem)
		curr = root
	outer:
		for i := range splitPath {
			var setFile *models.File
			isFile := i == len(splitPath)-1
			if isFile {
				setFile = file
			}

			path := join(splitPath[:i+1])

			var currNodeChildrenMap map[string]*Node[models.File]
			var isCurrNodeMapped bool

			if currNodeChildrenMap, isCurrNodeMapped = childrenMapsByNode[curr]; !isCurrNodeMapped {
				currNodeChildrenMap = make(map[string]*Node[models.File])
				childrenMapsByNode[curr] = currNodeChildrenMap
			}

			child, doesCurrNodeHaveChildAlready := currNodeChildrenMap[path]
			if doesCurrNodeHaveChildAlready {
				curr = child
				continue outer
			}

			if i == 0 && len(files) == 1 && len(splitPath) == 2 {
				// skip the root item when there's only one file at top level; we don't need it in that case
				continue outer
			}

			newChild := &Node[models.File]{
				path: path,
				File: setFile,
			}
			curr.Children = append(curr.Children, newChild)

			currNodeChildrenMap[path] = newChild

			curr = newChild
		}
	}

	root.Sort(cmp)
	root.Compress()

	return root
}

func BuildFlatTreeFromCommitFiles(
	files []*models.CommitFile,
	showRootItem bool,
	cmp func(a, b *Node[models.CommitFile]) int,
) *Node[models.CommitFile] {
	rootAux := BuildTreeFromCommitFiles(files, showRootItem, cmp)
	sortedFiles := rootAux.GetLeaves()

	return &Node[models.CommitFile]{Children: sortedFiles}
}

func BuildTreeFromCommitFiles(
	files []*models.CommitFile,
	showRootItem bool,
	cmp func(a, b *Node[models.CommitFile]) int,
) *Node[models.CommitFile] {
	root := &Node[models.CommitFile]{}

	var curr *Node[models.CommitFile]
	for _, file := range files {
		splitPath := SplitFileTreePath(file.Path, showRootItem)
		curr = root
	outer:
		for i := range splitPath {
			var setFile *models.CommitFile
			isFile := i == len(splitPath)-1
			if isFile {
				setFile = file
			}

			path := join(splitPath[:i+1])

			for _, existingChild := range curr.Children {
				if existingChild.path == path {
					curr = existingChild
					continue outer
				}
			}

			if i == 0 && len(files) == 1 && len(splitPath) == 2 {
				// skip the root item when there's only one file at top level; we don't need it in that case
				continue outer
			}

			newChild := &Node[models.CommitFile]{
				path: path,
				File: setFile,
			}
			curr.Children = append(curr.Children, newChild)

			curr = newChild
		}
	}

	root.Sort(cmp)
	root.Compress()

	return root
}

// BuildStatusSectionsFromFiles lists each file on its own line, grouped into
// staged, then unstaged, then untracked. A path with both staged and unstaged
// changes is listed under both of those headings. Merge conflicts stay in the
// unstaged group: they are not a clean staged change until they are resolved.
func BuildStatusSectionsFromFiles(
	files []*models.File,
	filter FileTreeDisplayFilter,
	caseSensitive bool,
	showRootItem bool,
) *Node[models.File] {
	grouped := map[StatusSection][]*Node[models.File]{}
	for _, file := range files {
		for _, section := range statusSectionsForFile(file, filter) {
			grouped[section] = append(grouped[section], &Node[models.File]{
				// Same internal path the directory tree uses, so toggling
				// between the two layouts can find the selected file again.
				File:          file,
				path:          InternalTreePathForFilePath(file.Path, showRootItem),
				StatusSection: section,
			})
		}
	}

	children := make([]*Node[models.File], 0, len(files))
	for _, section := range []StatusSection{StatusSectionStaged, StatusSectionUnstaged, StatusSectionUntracked} {
		nodes := grouped[section]
		sortStatusSectionNodes(nodes, caseSensitive)
		children = append(children, nodes...)
	}

	return &Node[models.File]{Children: children}
}

func statusSectionsForFile(file *models.File, filter FileTreeDisplayFilter) []StatusSection {
	switch filter {
	case DisplayStaged:
		return []StatusSection{StatusSectionStaged}
	case DisplayUnstaged:
		return []StatusSection{StatusSectionUnstaged}
	case DisplayUntracked:
		return []StatusSection{StatusSectionUntracked}
	case DisplayConflicted:
		return []StatusSection{StatusSectionUnstaged}
	case DisplayAll, DisplayTracked:
		// Decide per side below.
	}

	// A conflict is shown once, under Unstaged, unless the user asked for
	// staged files only (handled above).
	if file.HasMergeConflicts {
		return []StatusSection{StatusSectionUnstaged}
	}

	untracked := !file.Tracked && !file.HasStagedChanges
	includeStaged := file.HasStagedChanges && filter != DisplayUnstaged
	includeUnstaged := file.HasUnstagedChanges && !untracked && filter != DisplayStaged
	includeUntracked := untracked && filter != DisplayStaged && filter != DisplayTracked

	var sections []StatusSection
	if includeStaged {
		sections = append(sections, StatusSectionStaged)
	}
	if includeUnstaged {
		sections = append(sections, StatusSectionUnstaged)
	}
	if includeUntracked {
		sections = append(sections, StatusSectionUntracked)
	}
	if len(sections) == 0 {
		sections = append(sections, StatusSectionUntracked)
	}
	return sections
}

func sortStatusSectionNodes(nodes []*Node[models.File], caseSensitive bool) {
	sort.SliceStable(nodes, func(i, j int) bool {
		a := nodes[i].GetPath()
		b := nodes[j].GetPath()
		if !caseSensitive {
			a = strings.ToLower(a)
			b = strings.ToLower(b)
		}
		return a < b
	})
}

func BuildFlatTreeFromFiles(
	files []*models.File,
	showRootItem bool,
	cmp func(a, b *Node[models.File]) int,
) *Node[models.File] {
	rootAux := BuildTreeFromFiles(files, showRootItem, cmp)
	sortedFiles := rootAux.GetLeaves()

	// from top down we have merge conflict files, then tracked file, then untracked
	// files. This is the one way in which sorting differs between flat mode and
	// tree mode
	sort.SliceStable(sortedFiles, func(i, j int) bool {
		iFile := sortedFiles[i].File
		jFile := sortedFiles[j].File

		// never going to happen but just to be safe
		if iFile == nil || jFile == nil {
			return false
		}

		if iFile.HasMergeConflicts && !jFile.HasMergeConflicts {
			return true
		}

		if jFile.HasMergeConflicts && !iFile.HasMergeConflicts {
			return false
		}

		if iFile.Tracked && !jFile.Tracked {
			return true
		}

		if jFile.Tracked && !iFile.Tracked {
			return false
		}

		return false
	})

	return &Node[models.File]{Children: sortedFiles}
}

func split(str string) []string {
	return strings.Split(str, "/")
}

func join(strs []string) string {
	return strings.Join(strs, "/")
}

func SplitFileTreePath(path string, showRootItem bool) []string {
	return split(InternalTreePathForFilePath(path, showRootItem))
}

func InternalTreePathForFilePath(path string, showRootItem bool) string {
	if showRootItem {
		return "./" + path
	}

	return path
}
