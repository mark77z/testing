// Copyright 2014 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package repo

import (
	"bytes"
	"fmt"
	gotemplate "html/template"
	"io/ioutil"
	"path"
	"strings"

	"github.com/Unknwon/paginater"

	"github.com/gogits/git-module"

	"github.com/gogits/gogs/models"
	"github.com/gogits/gogs/modules/base"
	"github.com/gogits/gogs/modules/context"
	"github.com/gogits/gogs/modules/log"
	"github.com/gogits/gogs/modules/markdown"
	"github.com/gogits/gogs/modules/setting"
	"github.com/gogits/gogs/modules/template"
	"github.com/gogits/gogs/modules/template/highlight"
)

const (
	HOME     base.TplName = "repo/home"
	WATCHERS base.TplName = "repo/watchers"
	FORKS    base.TplName = "repo/forks"
)

func renderDirectory(ctx *context.Context, treeLink string) {
	tree, err := ctx.Repo.Commit.SubTree(ctx.Repo.TreePath)
	if err != nil {
		ctx.NotFoundOrServerError("Repo.Commit.SubTree", git.IsErrNotExist, err)
		return
	}

	entries, err := tree.ListEntries()
	if err != nil {
		ctx.Handle(500, "ListEntries", err)
		return
	}
	entries.Sort()

	ctx.Data["Files"], err = entries.GetCommitsInfo(ctx.Repo.Commit, ctx.Repo.TreePath)
	if err != nil {
		ctx.Handle(500, "GetCommitsInfo", err)
		return
	}

	var readmeFile *git.Blob
	for _, entry := range entries {
		if entry.IsDir() || !markdown.IsReadmeFile(entry.Name()) {
			continue
		}

		// TODO: collect all possible README files and show with priority.
		readmeFile = entry.Blob()
		break
	}

	if readmeFile != nil {
		ctx.Data["RawFileLink"] = ""
		ctx.Data["ReadmeInList"] = true
		ctx.Data["ReadmeExist"] = true

		dataRc, err := readmeFile.Data()
		if err != nil {
			ctx.Handle(500, "Data", err)
			return
		}

		buf := make([]byte, 1024)
		n, _ := dataRc.Read(buf)
		buf = buf[:n]

		isTextFile := base.IsTextFile(buf)
		ctx.Data["FileIsText"] = isTextFile
		ctx.Data["FileName"] = readmeFile.Name()
		// FIXME: what happens when README file is an image?
		if isTextFile {
			d, _ := ioutil.ReadAll(dataRc)
			buf = append(buf, d...)
			switch {
			case markdown.IsMarkdownFile(readmeFile.Name()):
				ctx.Data["IsMarkdown"] = true
				buf = markdown.Render(buf, treeLink, ctx.Repo.Repository.ComposeMetas())
			default:
				buf = bytes.Replace(buf, []byte("\n"), []byte(`<br>`), -1)
			}
			ctx.Data["FileContent"] = string(buf)
		}
	}

	// Show latest commit info of repository in table header,
	// or of directory if not in root directory.
	latestCommit := ctx.Repo.Commit
	if len(ctx.Repo.TreePath) > 0 {
		latestCommit, err = ctx.Repo.Commit.GetCommitByPath(ctx.Repo.TreePath)
		if err != nil {
			ctx.Handle(500, "GetCommitByPath", err)
			return
		}
	}
	ctx.Data["LatestCommit"] = latestCommit
	ctx.Data["LatestCommitUser"] = models.ValidateCommitWithEmail(latestCommit)

	// Check permission to add or upload new file.
	if ctx.Repo.IsWriter() && ctx.Repo.IsViewBranch {
		ctx.Data["CanAddFile"] = true
		ctx.Data["CanUploadFile"] = setting.Repository.Upload.Enabled
	}
}

func renderFile(ctx *context.Context, entry *git.TreeEntry, treeLink, rawLink string) {
	ctx.Data["IsViewFile"] = true

	blob := entry.Blob()
	dataRc, err := blob.Data()
	if err != nil {
		ctx.Handle(500, "Data", err)
		return
	}

	ctx.Data["FileSize"] = blob.Size()
	ctx.Data["FileName"] = blob.Name()
	ctx.Data["HighlightClass"] = highlight.FileNameToHighlightClass(blob.Name())
	ctx.Data["RawFileLink"] = rawLink + "/" + ctx.Repo.TreePath

	buf := make([]byte, 1024)
	n, _ := dataRc.Read(buf)
	buf = buf[:n]

	isTextFile := base.IsTextFile(buf)
	ctx.Data["IsTextFile"] = isTextFile

	// Assume file is not editable first.
	if !isTextFile {
		ctx.Data["EditFileTooltip"] = ctx.Tr("repo.editor.cannot_edit_non_text_files")
	}

	switch {
	case isTextFile:
		if blob.Size() >= setting.UI.MaxDisplayFileSize {
			ctx.Data["IsFileTooLarge"] = true
			break
		}

		d, _ := ioutil.ReadAll(dataRc)
		buf = append(buf, d...)

		isMarkdown := markdown.IsMarkdownFile(blob.Name())
		ctx.Data["IsMarkdown"] = isMarkdown

		readmeExist := isMarkdown || markdown.IsReadmeFile(blob.Name())
		ctx.Data["ReadmeExist"] = readmeExist
		if readmeExist {
			// TODO: don't need to render if it's a README but not Markdown file.
			ctx.Data["FileContent"] = string(markdown.Render(buf, path.Dir(treeLink), ctx.Repo.Repository.ComposeMetas()))
		} else {
			// Building code view blocks with line number on server side.
			var fileContent string
			if err, content := template.ToUTF8WithErr(buf); err != nil {
				if err != nil {
					log.Error(4, "ToUTF8WithErr: %s", err)
				}
				fileContent = string(buf)
			} else {
				fileContent = content
			}

			var output bytes.Buffer
			lines := strings.Split(fileContent, "\n")
			for index, line := range lines {
				output.WriteString(fmt.Sprintf(`<li class="L%d" rel="L%d">%s</li>`, index+1, index+1, gotemplate.HTMLEscapeString(line)) + "\n")
			}
			ctx.Data["FileContent"] = gotemplate.HTML(output.String())

			output.Reset()
			for i := 0; i < len(lines); i++ {
				output.WriteString(fmt.Sprintf(`<span id="L%d">%d</span>`, i+1, i+1))
			}
			ctx.Data["LineNums"] = gotemplate.HTML(output.String())
		}

		if ctx.Repo.CanEnableEditor() {
			ctx.Data["CanEditFile"] = true
			ctx.Data["EditFileTooltip"] = ctx.Tr("repo.editor.edit_this_file")
		} else if !ctx.Repo.IsViewBranch {
			ctx.Data["EditFileTooltip"] = ctx.Tr("repo.editor.must_be_on_a_branch")
		} else if !ctx.Repo.IsWriter() {
			ctx.Data["EditFileTooltip"] = ctx.Tr("repo.editor.fork_before_edit")
		}

	case base.IsPDFFile(buf):
		ctx.Data["IsPDFFile"] = true
	case base.IsImageFile(buf):
		ctx.Data["IsImageFile"] = true
	}

	if ctx.Repo.CanEnableEditor() {
		ctx.Data["CanDeleteFile"] = true
		ctx.Data["DeleteFileTooltip"] = ctx.Tr("repo.editor.delete_this_file")
	} else if !ctx.Repo.IsViewBranch {
		ctx.Data["DeleteFileTooltip"] = ctx.Tr("repo.editor.must_be_on_a_branch")
	} else if !ctx.Repo.IsWriter() {
		ctx.Data["DeleteFileTooltip"] = ctx.Tr("repo.editor.must_have_write_access")
	}
}

func Home(ctx *context.Context) {
	title := ctx.Repo.Repository.Owner.Name + "/" + ctx.Repo.Repository.Name
	ctx.Data["Title"] = title
	ctx.Data["PageIsViewCode"] = true
	ctx.Data["RequireHighlightJS"] = true

	branchLink := ctx.Repo.RepoLink + "/src/" + ctx.Repo.BranchName
	treeLink := branchLink
	rawLink := ctx.Repo.RepoLink + "/raw/" + ctx.Repo.BranchName

	if len(ctx.Repo.TreePath) > 0 {
		treeLink += "/" + ctx.Repo.TreePath
	}

	// Get current entry user currently looking at.
	entry, err := ctx.Repo.Commit.GetTreeEntryByPath(ctx.Repo.TreePath)
	if err != nil {
		ctx.NotFoundOrServerError("Repo.Commit.GetTreeEntryByPath", git.IsErrNotExist, err)
		return
	}

	if entry.IsDir() {
		renderDirectory(ctx, treeLink)
	} else {
		renderFile(ctx, entry, treeLink, rawLink)
	}
	if ctx.Written() {
		return
	}

	ec, err := ctx.Repo.GetEditorconfig()
	if err != nil && !git.IsErrNotExist(err) {
		ctx.Handle(500, "Repo.GetEditorconfig", err)
		return
	}
	ctx.Data["Editorconfig"] = ec

	var treeNames []string
	paths := make([]string, 0, 5)
	if len(ctx.Repo.TreePath) > 0 {
		treeNames = strings.Split(ctx.Repo.TreePath, "/")
		for i := range treeNames {
			paths = append(paths, strings.Join(treeNames[:i+1], "/"))
		}

		ctx.Data["HasParentPath"] = true
		if len(paths)-2 >= 0 {
			ctx.Data["ParentPath"] = "/" + paths[len(paths)-2]
		}
	}

	collaborators, err := ctx.Repo.Repository.GetCollaborators()
	if err != nil {
		log.Trace("GetCollaborators: %v", err)
	}

	prof, err := ctx.Repo.Repository.GetProfessor()
	if err != nil {
		log.Trace("No professor found")
		prof = nil
	}

	htags , err:= ctx.Repo.Repository.GetTags()
	if err != nil {
		log.Trace("No tags found")
		htags = nil
	}

	ctx.Data["Paths"] = paths
	ctx.Data["TreeLink"] = treeLink
	ctx.Data["TreeNames"] = treeNames
	ctx.Data["BranchLink"] = branchLink
	ctx.Data["Collaborators"] = collaborators
	ctx.Data["Professor"] = prof
	ctx.Data["HTags"] = htags
	ctx.HTML(200, HOME)
}

func RenderUserCards(ctx *context.Context, total int, getter func(page int) ([]*models.User, error), tpl base.TplName) {
	page := ctx.QueryInt("page")
	if page <= 0 {
		page = 1
	}
	pager := paginater.New(total, models.ItemsPerPage, page, 5)
	ctx.Data["Page"] = pager

	items, err := getter(pager.Current())
	if err != nil {
		ctx.Handle(500, "getter", err)
		return
	}
	ctx.Data["Cards"] = items

	ctx.HTML(200, tpl)
}

func Watchers(ctx *context.Context) {
	ctx.Data["Title"] = ctx.Tr("repo.watchers")
	ctx.Data["CardsTitle"] = ctx.Tr("repo.watchers")
	ctx.Data["PageIsWatchers"] = true
	RenderUserCards(ctx, ctx.Repo.Repository.NumWatches, ctx.Repo.Repository.GetWatchers, WATCHERS)
}

func Stars(ctx *context.Context) {
	ctx.Data["Title"] = ctx.Tr("repo.stargazers")
	ctx.Data["CardsTitle"] = ctx.Tr("repo.stargazers")
	ctx.Data["PageIsStargazers"] = true
	RenderUserCards(ctx, ctx.Repo.Repository.NumStars, ctx.Repo.Repository.GetStargazers, WATCHERS)
}

func Forks(ctx *context.Context) {
	ctx.Data["Title"] = ctx.Tr("repos.forks")

	forks, err := ctx.Repo.Repository.GetForks()
	if err != nil {
		ctx.Handle(500, "GetForks", err)
		return
	}

	for _, fork := range forks {
		if err = fork.GetOwner(); err != nil {
			ctx.Handle(500, "GetOwner", err)
			return
		}
	}
	ctx.Data["Forks"] = forks

	ctx.HTML(200, FORKS)
}
