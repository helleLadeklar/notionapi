package tohtml

import (
	"bytes"
	"fmt"
	"html"

	"path"
	"strings"

	"github.com/kjk/notionapi"
)

func maybePanic(format string, args ...interface{}) {
	notionapi.MaybePanic(format, args...)
}

func isSafeChar(r rune) bool {
	if r >= '0' && r <= '9' {
		return true
	}
	if r >= 'a' && r <= 'z' {
		return true
	}
	if r >= 'A' && r <= 'Z' {
		return true
	}
	return false
}

// safeName returns a file-system safe name
func safeName(s string) string {
	var res string
	for _, r := range s {
		if !isSafeChar(r) {
			res += " "
		} else {
			res += string(r)
		}
	}
	// replace multi-dash with single dash
	for strings.Contains(res, "  ") {
		res = strings.Replace(res, "  ", " ", -1)
	}
	res = strings.TrimLeft(res, " ")
	res = strings.TrimRight(res, " ")
	return res
}

func htmlFileName(title string) string {
	s := safeName(title)
	return s + ".html"
}

// HTMLFileNameForPage returns file name for html file
func HTMLFileNameForPage(page *notionapi.Page) string {
	return htmlFileName(page.Root().Title)
}
func log(format string, args ...interface{}) {
	notionapi.Log(format, args...)
}

// BlockRenderFunc is a function for rendering a particular block
type BlockRenderFunc func(block *notionapi.Block) bool

// Converter converts a Page to HTML
type Converter struct {
	// Buf is where HTML is being written to
	Buf  *bytes.Buffer
	Page *notionapi.Page

	// if true, adds id=${NotionID} attribute to HTML nodes
	AddIDAttribute bool

	// if true, adds <a href="#{$NotionID}">svg(anchor-icon)</a>
	// to h1/h2/h3
	AddHeaderAnchor bool

	// allows over-riding rendering of specific blocks
	// return false for default rendering
	RenderBlockOverride BlockRenderFunc

	// RewriteURL allows re-writing URLs e.g. to convert inter-notion URLs
	// to destination URLs
	RewriteURL func(url string) string

	// data provided by they caller, useful when providing
	// RenderBlockOverride
	Data interface{}

	// Level is current depth of the tree. Useuful for pretty-printing indentation
	Level int

	// we need this to properly render ordered and numbered lists
	CurrBlocks   []*notionapi.Block
	CurrBlockIdx int

	bufs []*bytes.Buffer
}

var (
	selfClosingTags = map[string]bool{
		"img": true,
	}
)

func isSelfClosing(tag string) bool {
	return selfClosingTags[tag]
}

// NewConverter returns customizable HTML renderer
func NewConverter(page *notionapi.Page) *Converter {
	return &Converter{
		Page: page,
	}
}

// PushNewBuffer creates a new buffer and sets Buf to it
func (c *Converter) PushNewBuffer() {
	c.bufs = append(c.bufs, c.Buf)
	c.Buf = &bytes.Buffer{}
}

// PopBuffer pops a buffer
func (c *Converter) PopBuffer() *bytes.Buffer {
	res := c.Buf
	n := len(c.bufs)
	c.Buf = c.bufs[n-1]
	c.bufs = c.bufs[:n-1]
	return res
}

// Newline writes a newline to the buffer. It'll suppress multiple newlines.
func (c *Converter) Newline() {
	d := c.Buf.Bytes()
	n := len(d)
	if n > 0 && d[n-1] != '\n' {
		c.Buf.WriteByte('\n')
	}
}

// WriteString writes a string to the buffer
func (c *Converter) WriteString(s string) {
	c.Buf.WriteString(s)
}

// WriteIndent writes 2 * Level spaces
func (c *Converter) WriteIndent() {
	if c.Level < 0 {
		panic("r.Level is < 0")
	}
	for n := 0; n < c.Level; n++ {
		c.WriteString("  ")
	}
}

func (c *Converter) maybeGetID(block *notionapi.Block) string {
	if c.AddIDAttribute {
		return notionapi.ToNoDashID(block.ID)
	}
	return ""
}

// WriteElement is a helper class that writes HTML with
// attributes and optional content
func (c *Converter) WriteElement(block *notionapi.Block, tag string, attrs []string, content string, entering bool) {
	if !entering {
		if !isSelfClosing(tag) {
			c.WriteIndent()
			c.WriteString("</" + tag + ">")
			c.Newline()
		}
		return
	}

	s := "<" + tag
	nAttrs := len(attrs) / 2
	for i := 0; i < nAttrs; i++ {
		a := attrs[i*2]
		// TODO: quote value if necessary
		v := attrs[i*2+1]
		s += fmt.Sprintf(` %s="%s"`, a, v)
	}
	id := c.maybeGetID(block)
	if id != "" {
		s += ` id="` + id + `"`
	}
	s += ">"
	c.WriteIndent()
	c.WriteString(s)
	c.Newline()
	if len(content) > 0 {
		c.WriteIndent()
		c.WriteString(content)
		c.Newline()
	} else {
		c.RenderInlines(block.InlineContent)
	}
	c.Newline()
}

// PrevBlock is a block preceding current block
func (c *Converter) PrevBlock() *notionapi.Block {
	if c.CurrBlockIdx == 0 {
		return nil
	}
	return c.CurrBlocks[c.CurrBlockIdx-1]
}

// NextBlock is a block preceding current block
func (c *Converter) NextBlock() *notionapi.Block {
	nextIdx := c.CurrBlockIdx + 1
	lastIdx := len(c.CurrBlocks) - 1
	if nextIdx > lastIdx {
		return nil
	}
	return c.CurrBlocks[nextIdx]
}

// IsPrevBlockOfType returns true if previous block is of a given type
func (c *Converter) IsPrevBlockOfType(t string) bool {
	b := c.PrevBlock()
	if b == nil {
		return false
	}
	return b.Type == t
}

// IsNextBlockOfType returns true if next block is of a given type
func (c *Converter) IsNextBlockOfType(t string) bool {
	b := c.NextBlock()
	if b == nil {
		return false
	}
	return b.Type == t
}

// FormatDate formats the data
func (c *Converter) FormatDate(d *notionapi.Date) string {
	// TODO: allow over-riding date formatting
	s := notionapi.FormatDate(d)
	return fmt.Sprintf(`<span class="notion-date">@%s</span>`, s)
}

// RenderInline renders inline block
func (c *Converter) RenderInline(b *notionapi.TextSpan) {
	var start, close string
	text := b.Text
	for _, attr := range b.Attrs {
		switch notionapi.AttrGetType(attr) {
		case notionapi.AttrBold:
			start += `<b>`
			close = close + `</b>`
		case notionapi.AttrItalic:
			start += `<i>`
			close = close + `</i>`
		case notionapi.AttrStrikeThrought:
			start += `<strike>`
			close = close + `</strike>`
		case notionapi.AttrCode:
			start += `<code class="notion-code-inline">`
			close = close + `</code>`
		case notionapi.AttrLink:
			uri := notionapi.AttrGetLink(attr)
			if c.RewriteURL != nil {
				uri = c.RewriteURL(uri)
			}
			text = html.EscapeString(text)
			s := fmt.Sprintf(`<a class="notion-link" href="%s">%s</a>`, uri, text)
			start += s
			text = ""
		case notionapi.AttrUser:
			userID := notionapi.AttrGetUserID(attr)
			userName := notionapi.ResolveUser(c.Page, userID)
			start += fmt.Sprintf(`<span class="notion-user">@%s</span>`, userName)
			text = ""
		case notionapi.AttrDate:
			date := notionapi.AttrGetDate(attr)
			start += c.FormatDate(date)
			text = ""
		}
	}
	start += html.EscapeString(text)
	c.WriteString(start + close)
}

// RenderInlines renders inline blocks
func (c *Converter) RenderInlines(blocks []*notionapi.TextSpan) {
	c.Level++
	c.WriteIndent()
	for _, block := range blocks {
		c.RenderInline(block)
	}

	c.Level--
}

// GetInlineContent is like RenderInlines but instead of writing to
// output buffer, we return it as string
func (c *Converter) GetInlineContent(blocks []*notionapi.TextSpan) string {
	if len(blocks) == 0 {
		return ""
	}
	c.PushNewBuffer()
	for _, block := range blocks {
		c.RenderInline(block)
	}
	return c.PopBuffer().String()
}

// RenderCode renders BlockCode
func (c *Converter) RenderCode(block *notionapi.Block) {
	cls := "notion-code"
	lang := strings.ToLower(strings.TrimSpace(block.CodeLanguage))
	if lang != "" {
		cls += " notion-lang-" + lang
	}
	code := html.EscapeString(block.Code)
	s := fmt.Sprintf(`<pre class="%s"><code>%s`, cls, code)
	c.WriteString(s)

	c.WriteString("</code></pre>")
	c.Newline()
}

// RenderPage renders BlockPage
func (c *Converter) RenderPage(block *notionapi.Block) {
	tp := block.GetPageType()
	if tp == notionapi.BlockPageTopLevel {
		title := html.EscapeString(block.Title)
		content := fmt.Sprintf(`<div class="notion-page-content">%s</div>`, title)
		attrs := []string{"class", "notion-page"}
		c.WriteElement(block, "div", attrs, content, true)
		c.RenderChildren(block)
		c.WriteElement(block, "div", attrs, content, false)
		return
	}

	cls := "notion-page-link"
	if tp == notionapi.BlockPageSubPage {
		cls = "notion-sub-page"
	}
	id := notionapi.ToNoDashID(block.ID)
	uri := "https://notion.so/" + id
	title := html.EscapeString(block.Title)
	s := fmt.Sprintf(`<div class="%s"><a href="%s">%s</a></div>`, cls, uri, title)
	c.WriteIndent()
	c.WriteString(s)
	c.Newline()
}

// RenderText renders BlockText
func (c *Converter) RenderText(block *notionapi.Block) {
	attrs := []string{"class", "notion-text"}
	c.WriteElement(block, "div", attrs, "", true)
	c.RenderChildren(block)
	c.WriteElement(block, "div", attrs, "", false)
}

// RenderNumberedList renders BlockNumberedList
func (c *Converter) RenderNumberedList(block *notionapi.Block) {
	isPrevSame := c.IsPrevBlockOfType(notionapi.BlockNumberedList)
	if !isPrevSame {
		c.WriteIndent()
		c.WriteString(`<ol class="notion-numbered-list">`)
	}
	attrs := []string{"class", "notion-numbered-list"}
	c.WriteElement(block, "li", attrs, "", true)

	c.RenderChildren(block)

	c.WriteIndent()
	c.WriteString(`</li>`)
	isNextSame := c.IsNextBlockOfType(notionapi.BlockNumberedList)
	if !isNextSame {
		c.WriteIndent()
		c.WriteString(`</ol>`)
	}
	c.Newline()
}

// RenderBulletedList renders BlockBulletedList
func (c *Converter) RenderBulletedList(block *notionapi.Block) {

	isPrevSame := c.IsPrevBlockOfType(notionapi.BlockBulletedList)
	if !isPrevSame {
		c.WriteIndent()
		c.WriteString(`<ul class="notion-bulleted-list">`)
		c.Newline()
		c.Level++
	}
	attrs := []string{"class", "notion-bulleted-list"}
	c.WriteElement(block, "li", attrs, "", true)

	c.RenderChildren(block)

	c.WriteIndent()
	c.WriteString(`</li>`)
	isNextSame := c.IsNextBlockOfType(notionapi.BlockBulletedList)
	if !isNextSame {
		c.Level--
		c.Newline()
		c.WriteIndent()
		c.WriteString(`</ul>`)
	}
	c.Newline()
}

// RenderHeaderLevel renders BlockHeader, SubHeader and SubSubHeader
func (c *Converter) RenderHeaderLevel(block *notionapi.Block, level int) {
	el := fmt.Sprintf("h%d", level)
	cls := fmt.Sprintf("notion-header-%d", level)
	attrs := []string{"class", cls}
	content := c.GetInlineContent(block.InlineContent)
	id := c.maybeGetID(block)
	if c.AddHeaderAnchor {
		content += fmt.Sprintf(` <a class="notion-header-anchor" href="#%s" aria-hidden="true"><svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 8 8"><path d="M5.88.03c-.18.01-.36.03-.53.09-.27.1-.53.25-.75.47a.5.5 0 1 0 .69.69c.11-.11.24-.17.38-.22.35-.12.78-.07 1.06.22.39.39.39 1.04 0 1.44l-1.5 1.5c-.44.44-.8.48-1.06.47-.26-.01-.41-.13-.41-.13a.5.5 0 1 0-.5.88s.34.22.84.25c.5.03 1.2-.16 1.81-.78l1.5-1.5c.78-.78.78-2.04 0-2.81-.28-.28-.61-.45-.97-.53-.18-.04-.38-.04-.56-.03zm-2 2.31c-.5-.02-1.19.15-1.78.75l-1.5 1.5c-.78.78-.78 2.04 0 2.81.56.56 1.36.72 2.06.47.27-.1.53-.25.75-.47a.5.5 0 1 0-.69-.69c-.11.11-.24.17-.38.22-.35.12-.78.07-1.06-.22-.39-.39-.39-1.04 0-1.44l1.5-1.5c.4-.4.75-.45 1.03-.44.28.01.47.09.47.09a.5.5 0 1 0 .44-.88s-.34-.2-.84-.22z"></path></svg></a>`, id)
	}
	c.WriteElement(block, el, attrs, content, true)
	c.WriteElement(block, el, attrs, "", false)
}

// RenderHeader renders BlockHeader
func (c *Converter) RenderHeader(block *notionapi.Block) {
	c.RenderHeaderLevel(block, 1)
}

// RenderSubHeader renders BlockSubHeader
func (c *Converter) RenderSubHeader(block *notionapi.Block) {
	c.RenderHeaderLevel(block, 2)
}

// RenderSubSubHeader renders BlocSubSubkHeader
func (c *Converter) RenderSubSubHeader(block *notionapi.Block) {
	c.RenderHeaderLevel(block, 3)
}

// RenderTodo renders BlockTodo
func (c *Converter) RenderTodo(block *notionapi.Block) {
	cls := "notion-todo"
	if block.IsChecked {
		cls = "notion-todo-checked"
	}
	attrs := []string{"class", cls}
	c.WriteElement(block, "div", attrs, "", true)

	c.RenderChildren(block)

	c.WriteElement(block, "div", attrs, "", false)
}

// RenderToggle renders BlockToggle
func (c *Converter) RenderToggle(block *notionapi.Block) {
	s := `<details class="notion-toggle"`
	id := c.maybeGetID(block)
	if id != "" {
		s += fmt.Sprintf(` id="%s"`, id)
	}
	c.WriteString(s + `>`)
	c.Newline()

	// we don't want id on summary but on <details> above
	prevAddID := c.AddIDAttribute
	c.AddIDAttribute = false
	c.WriteElement(block, "summary", nil, "", true)
	c.WriteString(`</summary>`)
	c.AddIDAttribute = prevAddID

	c.Newline()

	c.RenderChildren(block)

	c.WriteString("</details>\n")
}

// RenderQuote renders BlockQuote
func (c *Converter) RenderQuote(block *notionapi.Block) {
	cls := "notion-quote"
	attrs := []string{"class", cls}
	c.WriteElement(block, "quote", attrs, "", true)

	c.RenderChildren(block)

	c.WriteElement(block, "quote", attrs, "", false)
}

// RenderCallout renders BlockCallout
func (c *Converter) RenderCallout(block *notionapi.Block) {
	cls := "notion-callout"
	attrs := []string{"class", cls}
	c.WriteElement(block, "div", attrs, "", true)

	c.RenderChildren(block)
	c.WriteElement(block, "div", attrs, "", false)
}

// RenderTableOfContents renders BlockTableOfContents
func (c *Converter) RenderTableOfContents(block *notionapi.Block) {
	// TODO: implement me
}

// RenderDivider renders BlockDivider
func (c *Converter) RenderDivider(block *notionapi.Block) {
	c.WriteString(`<hr class="notion-divider">` + "\n")
}

// RenderBookmark renders BlockBookmark
func (c *Converter) RenderBookmark(block *notionapi.Block) {
	content := fmt.Sprintf(`<a href="%s">%s</a>`, block.Link, block.Link)
	cls := "notion-bookmark"
	// TODO: don't render inlines (which seems to be title of the bookmarked page)
	// TODO: support caption
	// TODO: maybe support comments
	attrs := []string{"class", cls}
	c.WriteElement(block, "div", attrs, content, true)
	c.WriteElement(block, "div", attrs, content, false)
}

// RenderVideo renders BlockTweet
func (c *Converter) RenderVideo(block *notionapi.Block) {
	f := block.FormatVideo()
	ws := fmt.Sprintf("%d", f.BlockWidth)
	uri := f.DisplaySource
	if uri == "" {
		// TODO: not sure if this is needed
		uri = block.Source
	}
	// TODO: get more info from format
	attrs := []string{
		"class", "notion-video",
		"width", ws,
		"src", uri,
		"frameborder", "0",
		"allow", "encrypted-media",
		"allowfullscreen", "true",
	}
	// TODO: can it be that f.BlockWidth is 0 and we need to
	// calculate it from f.BlockHeight
	h := f.BlockHeight
	if h == 0 {
		h = int64(float64(f.BlockWidth) * f.BlockAspectRatio)
	}
	if h > 0 {
		hs := fmt.Sprintf("%d", h)
		attrs = append(attrs, "height", hs)
	}

	c.WriteElement(block, "iframe", attrs, "", true)
	c.WriteElement(block, "iframe", attrs, "", false)
}

// RenderTweet renders BlockTweet
func (c *Converter) RenderTweet(block *notionapi.Block) {
	uri := block.Source
	content := fmt.Sprintf(`Embedded tweet <a href="%s">%s</a>`, uri, uri)
	cls := "notion-embed"
	// TODO: don't render inlines (which seems to be title of the bookmarked page)
	// TODO: support caption
	// TODO: maybe support comments
	attrs := []string{"class", cls}
	c.WriteElement(block, "div", attrs, content, true)
	c.WriteElement(block, "div", attrs, content, false)
}

// RenderGist renders BlockGist
func (c *Converter) RenderGist(block *notionapi.Block) {
	uri := block.Source + ".js"
	cls := "notion-embed-gist"
	attrs := []string{"src", uri, "class", cls}
	// TODO: support caption
	// TODO: maybe support comments
	c.WriteElement(block, "script", attrs, "", true)
	c.WriteElement(block, "script", attrs, "", false)
}

// RenderEmbed renders BlockEmbed
func (c *Converter) RenderEmbed(block *notionapi.Block) {
	// TODO: best effort at making the URL readable
	f := block.FormatEmbed()
	uri := ""
	if f != nil {
		uri = f.DisplaySource
	}
	title := block.Title
	if title == "" {
		title = path.Base(uri)
	}
	title = html.EscapeString(title)
	content := fmt.Sprintf(`Oembed: <a href="%s">%s</a>`, uri, title)
	cls := "notion-embed"
	attrs := []string{"class", cls}
	c.WriteElement(block, "div", attrs, content, true)
	c.WriteElement(block, "div", attrs, content, false)
}

// RenderFile renders BlockFile
func (c *Converter) RenderFile(block *notionapi.Block) {
	// TODO: best effort at making the URL readable
	uri := block.Source
	title := block.Title
	if title == "" {
		title = path.Base(uri)
	}
	title = html.EscapeString(title)
	content := fmt.Sprintf(`Embedded file: <a href="%s">%s</a>`, uri, title)
	cls := "notion-embed"
	attrs := []string{"class", cls}
	c.WriteElement(block, "div", attrs, content, true)
	c.WriteElement(block, "div", attrs, content, false)
}

// RenderPDF renders BlockPDF
func (c *Converter) RenderPDF(block *notionapi.Block) {
	// TODO: best effort at making the URL readable
	uri := block.Source
	title := block.Title
	if title == "" {
		title = path.Base(uri)
	}
	title = html.EscapeString(title)
	content := fmt.Sprintf(`Embedded PDF: <a href="%s">%s</a>`, uri, title)
	cls := "notion-embed"
	attrs := []string{"class", cls}
	c.WriteElement(block, "div", attrs, content, true)
	c.WriteElement(block, "div", attrs, content, false)
}

// RenderImage renders BlockImage
func (c *Converter) RenderImage(block *notionapi.Block) {
	link := block.ImageURL
	attrs := []string{"class", "notion-image", "src", link}
	c.WriteElement(block, "img", attrs, "", true)
	c.WriteElement(block, "img", attrs, "", false)
}

// RenderColumnList renders BlockColumnList
// it's children are BlockColumn
func (c *Converter) RenderColumnList(block *notionapi.Block) {
	nColumns := len(block.Content)
	if nColumns == 0 {
		maybePanic("has no columns")
		return
	}
	attrs := []string{"class", "notion-column-list"}
	c.WriteElement(block, "div", attrs, "", true)
	c.RenderChildren(block)
	c.WriteElement(block, "div", attrs, "", false)
}

// RenderColumn renders BlockColumn
// it's parent is BlockColumnList
func (c *Converter) RenderColumn(block *notionapi.Block) {
	// TODO: get column ration from col.FormatColumn.ColumnRation, which is float 0...1
	attrs := []string{"class", "notion-column"}
	c.WriteElement(block, "div", attrs, "", true)
	c.RenderChildren(block)
	c.WriteElement(block, "div", attrs, "", false)
}

// RenderCollectionView renders BlockCollectionView
func (c *Converter) RenderCollectionView(block *notionapi.Block) {
	viewInfo := block.CollectionViews[0]
	view := viewInfo.CollectionView
	if view.Format == nil {
		id := ""
		if c.Page != nil {
			id = notionapi.ToNoDashID(c.Page.ID)
		}
		log("missing view.Format for block %s %s in page %s\n", block.ID, block.Type, id)
		return
	}
	columns := view.Format.TableProperties

	c.Newline()
	c.WriteIndent()
	c.WriteString("\n" + `<table class="notion-collection-view">` + "\n")

	// generate header row
	c.Level++
	c.WriteIndent()
	c.WriteString("<thead>\n")

	c.Level++
	c.WriteIndent()
	c.WriteString("<tr>\n")

	for _, col := range columns {
		colName := col.Property
		colInfo := viewInfo.Collection.CollectionSchema[colName]
		if colInfo != nil {
			name := colInfo.Name
			c.Level++
			c.WriteIndent()
			c.WriteString(`<th>` + html.EscapeString(name) + "</th>\n")
			c.Level--
		} else {
			c.Level++
			c.WriteIndent()
			c.WriteString(`<th>&nbsp;` + "</th>\n")
			c.Level--
		}
	}
	c.WriteIndent()
	c.WriteString("</tr>\n")

	c.Level--
	c.WriteIndent()
	c.WriteString("</thead>\n\n")

	c.WriteIndent()
	c.WriteString("<tbody>\n")

	for _, row := range viewInfo.CollectionRows {
		c.Level++
		c.WriteIndent()
		c.WriteString("<tr>\n")

		props := row.Properties
		for _, col := range columns {
			colName := col.Property
			v := props[colName]
			//fmt.Printf("inline: '%s'\n", fmt.Sprintf("%v", v))
			inlineContent, err := notionapi.ParseTextSpans(v)
			if err != nil {
				maybePanic("ParseTextSpans of '%v' failed with %s\n", v, err)
			}
			//pretty.Print(inlineContent)
			colVal := c.GetInlineContent(inlineContent)
			//fmt.Printf("colVal: '%s'\n", colVal)
			c.Level++
			c.WriteIndent()
			//colInfo := viewInfo.Collection.CollectionSchema[colName]
			// TODO: format colVal according to colInfo
			c.WriteString(`<td>` + colVal + `</td>`)
			c.Newline()
			c.Level--
		}
		c.WriteIndent()
		c.WriteString("</tr>\n")
		c.Level--
	}

	c.WriteIndent()
	c.WriteString("</tbody>\n")

	c.Level--
	c.WriteIndent()
	c.WriteString("</table>\n")
}

// DefaultRenderFunc returns a defult rendering function for a type of
// a given block
func (c *Converter) DefaultRenderFunc(blockType string) func(*notionapi.Block) {
	switch blockType {
	case notionapi.BlockPage:
		return c.RenderPage
	case notionapi.BlockText:
		return c.RenderText
	case notionapi.BlockNumberedList:
		return c.RenderNumberedList
	case notionapi.BlockBulletedList:
		return c.RenderBulletedList
	case notionapi.BlockHeader:
		return c.RenderHeader
	case notionapi.BlockSubHeader:
		return c.RenderSubHeader
	case notionapi.BlockSubSubHeader:
		return c.RenderSubSubHeader
	case notionapi.BlockTodo:
		return c.RenderTodo
	case notionapi.BlockToggle:
		return c.RenderToggle
	case notionapi.BlockQuote:
		return c.RenderQuote
	case notionapi.BlockDivider:
		return c.RenderDivider
	case notionapi.BlockCode:
		return c.RenderCode
	case notionapi.BlockBookmark:
		return c.RenderBookmark
	case notionapi.BlockImage:
		return c.RenderImage
	case notionapi.BlockColumnList:
		return c.RenderColumnList
	case notionapi.BlockColumn:
		return c.RenderColumn
	case notionapi.BlockCollectionView:
		return c.RenderCollectionView
	case notionapi.BlockEmbed:
		return c.RenderEmbed
	case notionapi.BlockGist:
		return c.RenderGist
	case notionapi.BlockTweet:
		return c.RenderTweet
	case notionapi.BlockVideo:
		return c.RenderVideo
	case notionapi.BlockFile:
		return c.RenderFile
	case notionapi.BlockPDF:
		return c.RenderPDF
	case notionapi.BlockCallout:
		return c.RenderCallout
	case notionapi.BlockTableOfContents:
		return c.RenderTableOfContents
	default:
		maybePanic("DefaultRenderFunc: unsupported block type '%s' in %s\n", blockType, c.Page.NotionURL())
	}
	return nil
}

func needsWrapper(block *notionapi.Block) bool {
	if len(block.Content) == 0 {
		return false
	}
	switch block.Type {
	// TODO: maybe more block types need this
	case notionapi.BlockText:
		return true
	}
	return false
}

func (c *Converter) RenderChildren(block *notionapi.Block) {
	if len(block.Content) == 0 {
		return
	}

	// .notion-wrap provides indentation for children
	if needsWrapper(block) {
		c.Newline()
		c.WriteIndent()
		c.WriteString(`<div class="notion-wrap">`)
		c.Newline()
	}

	c.Level++
	currIdx := c.CurrBlockIdx
	currBlocks := c.CurrBlocks
	c.CurrBlocks = block.Content
	for i, child := range block.Content {
		child.Parent = block
		c.CurrBlockIdx = i
		c.RenderBlock(child)
	}
	c.CurrBlockIdx = currIdx
	c.CurrBlocks = currBlocks
	c.Level--

	if needsWrapper(block) {
		c.Newline()
		c.WriteIndent()
		c.WriteString(`</div>`)
		c.Newline()
	}
}

// RenderBlock renders a block to html
func (c *Converter) RenderBlock(block *notionapi.Block) {
	if block == nil {
		// a missing block
		return
	}
	if c.RenderBlockOverride != nil {
		handled := c.RenderBlockOverride(block)
		if handled {
			return
		}
	}
	def := c.DefaultRenderFunc(block.Type)
	if def != nil {
		def(block)
	}
}

// ToHTML renders a page to html
func (c *Converter) ToHTML() []byte {
	c.Level = 0
	c.PushNewBuffer()

	c.RenderBlock(c.Page.Root())
	buf := c.PopBuffer()
	if c.Level != 0 {
		panic(fmt.Sprintf("r.Level is %d, should be 0", c.Level))
	}
	return buf.Bytes()
}

// ToHTML converts a page to HTML
func ToHTML(page *notionapi.Page) []byte {
	r := NewConverter(page)
	return r.ToHTML()
}
