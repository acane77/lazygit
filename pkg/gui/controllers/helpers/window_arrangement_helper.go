package helpers

import (
	"fmt"
	mapsPkg "maps"
	"math"
	"strings"

	"github.com/jesseduffield/lazycore/pkg/boxlayout"
	"github.com/jesseduffield/lazygit/pkg/config"
	"github.com/jesseduffield/lazygit/pkg/gui/types"
	"github.com/jesseduffield/lazygit/pkg/utils"
	"golang.org/x/exp/slices"
)

// In this file we use the boxlayout package, along with knowledge about the app's state,
// to arrange the windows (i.e. panels) on the screen.

type WindowArrangementHelper struct {
	c               *HelperCommon
	windowHelper    *WindowHelper
	modeHelper      *ModeHelper
	appStatusHelper *AppStatusHelper
}

func NewWindowArrangementHelper(
	c *HelperCommon,
	windowHelper *WindowHelper,
	modeHelper *ModeHelper,
	appStatusHelper *AppStatusHelper,
) *WindowArrangementHelper {
	return &WindowArrangementHelper{
		c:               c,
		windowHelper:    windowHelper,
		modeHelper:      modeHelper,
		appStatusHelper: appStatusHelper,
	}
}

type WindowArrangementArgs struct {
	// Width of the screen (in characters)
	Width int
	// Height of the screen (in characters)
	Height int
	// User config
	UserConfig *config.UserConfig
	// Name of the currently focused window. (It's actually the current static window, meaning
	// popups are ignored)
	CurrentWindow string
	// Name of the current side window (i.e. the current window in the left
	// section of the UI)
	CurrentSideWindow string
	// Returns the view currently shown in the given window. When a window holds
	// several tabbed views this is the selected tab, which is what the status and
	// stash height special-cases key off (rather than the window itself, whose
	// name is just its first tab).
	ActiveViewForWindow func(window string) string
	// Returns the number of content lines of the view currently shown in the given
	// window. Used by the shrink-to-content feature to size a panel to its content.
	ContentHeightForWindow func(window string) int
	// Whether the main panel is split (as is the case e.g. when a file has both
	// staged and unstaged changes)
	MainPanes types.MainPanes
	// The current screen mode (normal, half, full)
	ScreenMode types.ScreenMode
	// The content shown on the bottom left of the screen when showing a loader
	// or toast e.g. 'Rebasing /'
	AppStatus string
	// The content shown on the bottom right of the screen (e.g. the 'donate',
	// 'ask question' links or a message about the current mode e.g. rebase mode)
	InformationStr string
	// Whether to show the extras window which contains the command log context
	ShowExtrasWindow bool
	// Whether we are in a demo (which is used for generating demo gifs for the
	// repo's readme)
	InDemo bool
	// Whether any mode is active (e.g. rebasing, cherry picking, etc)
	IsAnyModeActive bool
	// Whether the search prompt is shown in the bottom left
	InSearchPrompt bool
	// One of '' (not searching), 'Search: ', and 'Filter: '
	SearchPrefix string
}

func (self *WindowArrangementHelper) GetWindowDimensions(informationStr string, appStatus string) map[string]boxlayout.Dimensions {
	width, height := self.c.GocuiGui().Size()
	repoState := self.c.State().GetRepoState()

	var searchPrefix string
	if _, ok := repoState.GetSearchState().Context.(types.IFilterableContext); ok {
		searchPrefix = self.c.Tr.FilterPrefix
	} else {
		searchPrefix = self.c.Tr.SearchPrefix
	}

	args := WindowArrangementArgs{
		Width:               width,
		Height:              height,
		UserConfig:          self.c.UserConfig(),
		CurrentWindow:       self.c.Context().CurrentStatic().GetWindowName(),
		CurrentSideWindow:   self.c.Context().CurrentSide().GetWindowName(),
		ActiveViewForWindow: self.windowHelper.GetViewNameForWindow,
		ContentHeightForWindow: func(window string) int {
			return self.windowHelper.GetContextForWindow(window).TotalContentHeight()
		},
		MainPanes:        repoState.GetMainPanes(),
		ScreenMode:       repoState.GetScreenMode(),
		AppStatus:        appStatus,
		InformationStr:   informationStr,
		ShowExtrasWindow: self.c.State().GetShowExtrasWindow(),
		InDemo:           self.c.InDemo(),
		IsAnyModeActive:  self.modeHelper.IsAnyModeActive(),
		InSearchPrompt:   repoState.InSearchPrompt(),
		SearchPrefix:     searchPrefix,
	}

	return GetWindowDimensions(args)
}

func shouldUsePortraitMode(args WindowArrangementArgs) bool {
	if args.ScreenMode == types.SCREEN_HALF {
		return args.UserConfig.Gui.EnlargedSideViewLocation == "top"
	}

	switch args.UserConfig.Gui.PortraitMode {
	case "never":
		return false
	case "always":
		return true
	default: // "auto" or any garbage values in PortraitMode value
		return args.Width <= args.UserConfig.Gui.PortraitModeAutoMaxWidth &&
			args.Height >= args.UserConfig.Gui.PortraitModeAutoMinHeight
	}
}

func GetWindowDimensions(args WindowArrangementArgs) map[string]boxlayout.Dimensions {
	sideSectionWeight, mainSectionWeight := getMidSectionWeights(args)

	sidePanelsDirection := boxlayout.COLUMN
	if shouldUsePortraitMode(args) {
		sidePanelsDirection = boxlayout.ROW
	}

	showInfoSection := args.UserConfig.Gui.ShowBottomLine ||
		args.InSearchPrompt ||
		args.IsAnyModeActive ||
		args.AppStatus != ""
	infoSectionSize := 0
	if showInfoSection {
		infoSectionSize = 1
	}

	root := &boxlayout.Box{
		Direction: boxlayout.ROW,
		Children: []*boxlayout.Box{
			{
				Direction: sidePanelsDirection,
				Weight:    1,
				Children: []*boxlayout.Box{
					{
						Direction:           boxlayout.ROW,
						Weight:              sideSectionWeight,
						ConditionalChildren: sidePanelChildren(args),
					},
					{
						Direction: boxlayout.ROW,
						Weight:    mainSectionWeight,
						Children:  mainPanelChildren(args),
					},
				},
			},
			{
				Direction: boxlayout.COLUMN,
				Size:      infoSectionSize,
				Children:  infoSectionChildren(args),
			},
		},
	}

	layerOneWindows := boxlayout.ArrangeWindows(root, 0, 0, args.Width, args.Height)
	limitWindows := boxlayout.ArrangeWindows(&boxlayout.Box{Window: "limit"}, 0, 0, args.Width, args.Height)

	return MergeMaps(layerOneWindows, limitWindows)
}

func mainPanelChildren(args WindowArrangementArgs) []*boxlayout.Box {
	mainPanelsDirection := boxlayout.ROW
	if splitMainPanelSideBySide(args) {
		mainPanelsDirection = boxlayout.COLUMN
	}

	// The commits window sits above the diff, about 30% of this column, so the
	// left column can give its spare height to the files panel.
	commitsOnRight := commitsWindowOnRight(args.UserConfig)
	fullScreen := args.ScreenMode == types.SCREEN_FULL
	commitsTakesOver := fullScreen && commitsOnRight && args.CurrentWindow == "commits"
	diffTakesOver := fullScreen && (args.CurrentWindow == "main" || args.CurrentWindow == "secondary")

	result := []*boxlayout.Box{}
	if commitsOnRight && !diffTakesOver {
		weight := 3
		if commitsTakesOver {
			weight = 1
		}
		result = append(result, &boxlayout.Box{Window: "commits", Weight: weight})
	}
	if !commitsTakesOver {
		weight := 1
		if commitsOnRight && !diffTakesOver {
			weight = 7
		}
		result = append(result, &boxlayout.Box{
			Direction: mainPanelsDirection,
			Children:  mainSectionChildren(args),
			Weight:    weight,
		})
	}
	if args.ShowExtrasWindow && !commitsTakesOver {
		result = append(result, &boxlayout.Box{
			Window: "extras",
			Size:   getExtrasWindowSize(args),
		})
	}
	return result
}

// commitsWindowOnRight reports whether the commits panel is its own window, in
// which case it is drawn above the diff instead of in the left column.
func commitsWindowOnRight(userConfig *config.UserConfig) bool {
	for _, panel := range userConfig.Gui.SidePanels {
		if len(panel) > 0 && panel[0] == "commits" {
			return true
		}
	}
	return false
}

// leftSideWindowNames is the left column. The commits window is omitted when it
// is drawn above the diff.
func leftSideWindowNames(userConfig *config.UserConfig) []string {
	windows := sideWindowNames(userConfig)
	if !commitsWindowOnRight(userConfig) {
		return windows
	}
	filtered := make([]string, 0, len(windows))
	for _, window := range windows {
		if window != "commits" {
			filtered = append(filtered, window)
		}
	}
	return filtered
}

func MergeMaps[K comparable, V any](maps ...map[K]V) map[K]V {
	result := map[K]V{}
	for _, currMap := range maps {
		mapsPkg.Copy(result, currMap)
	}

	return result
}

func mainSectionChildren(args WindowArrangementArgs) []*boxlayout.Box {
	mainPane := &boxlayout.Box{Window: "main", Weight: 1}
	secondaryPane := &boxlayout.Box{Window: "secondary", Weight: 1}

	switch args.MainPanes {
	case types.MainPaneOnly:
		return []*boxlayout.Box{mainPane}
	case types.SecondaryPaneOnly:
		return []*boxlayout.Box{secondaryPane}
	case types.BothMainPanes:
		// In full-screen mode the focused one takes the whole section anyway.
		if args.ScreenMode == types.SCREEN_FULL {
			if args.CurrentWindow == "main" {
				return []*boxlayout.Box{mainPane}
			}
			if args.CurrentWindow == "secondary" {
				return []*boxlayout.Box{secondaryPane}
			}
		}
	}

	return []*boxlayout.Box{mainPane, secondaryPane}
}

func getMidSectionWeights(args WindowArrangementArgs) (int, int) {
	sidePanelWidthRatio := args.UserConfig.Gui.SidePanelWidth
	// Using 120 so that the default of 0.3333 will remain consistent with previous behavior
	const maxColumnCount = 120
	mainSectionWeight := int(math.Round(maxColumnCount * (1 - sidePanelWidthRatio)))
	sideSectionWeight := int(math.Round(maxColumnCount * sidePanelWidthRatio))

	if splitMainPanelSideBySide(args) {
		mainSectionWeight = sideSectionWeight * 5 // need to shrink side panel to make way for main panels if side-by-side
	}

	if args.CurrentWindow == "main" || args.CurrentWindow == "secondary" ||
		(args.CurrentWindow == "commits" && commitsWindowOnRight(args.UserConfig)) {
		if args.ScreenMode == types.SCREEN_HALF || args.ScreenMode == types.SCREEN_FULL {
			sideSectionWeight = 0
		}
	} else {
		if args.ScreenMode == types.SCREEN_HALF {
			if args.UserConfig.Gui.EnlargedSideViewLocation == "top" {
				mainSectionWeight = sideSectionWeight * 2
			} else {
				mainSectionWeight = sideSectionWeight
			}
		} else if args.ScreenMode == types.SCREEN_FULL {
			mainSectionWeight = 0
		}
	}

	return sideSectionWeight, mainSectionWeight
}

func infoSectionChildren(args WindowArrangementArgs) []*boxlayout.Box {
	if args.InSearchPrompt {
		return []*boxlayout.Box{
			{
				Window: "searchPrefix",
				Size:   utils.StringWidth(args.SearchPrefix),
			},
			{
				Window: "search",
				Weight: 1,
			},
		}
	}

	statusSpacerPrefix := "statusSpacer"
	spacerBoxIndex := 0
	maxSpacerBoxIndex := 2 // See pkg/gui/types/views.go
	// Returns a box with size 1 to be used as padding between views
	spacerBox := func() *boxlayout.Box {
		spacerBoxIndex++

		if spacerBoxIndex > maxSpacerBoxIndex {
			panic("Too many spacer boxes")
		}

		return &boxlayout.Box{Window: fmt.Sprintf("%s%d", statusSpacerPrefix, spacerBoxIndex), Size: 1}
	}

	// Returns a box with weight 1 to be used as flexible padding between views
	flexibleSpacerBox := func() *boxlayout.Box {
		spacerBoxIndex++

		if spacerBoxIndex > maxSpacerBoxIndex {
			panic("Too many spacer boxes")
		}

		return &boxlayout.Box{Window: fmt.Sprintf("%s%d", statusSpacerPrefix, spacerBoxIndex), Weight: 1}
	}

	// Adds spacer boxes inbetween given boxes
	insertSpacerBoxes := func(boxes []*boxlayout.Box) []*boxlayout.Box {
		for i := len(boxes) - 1; i >= 1; i-- {
			// ignore existing spacer boxes
			if !strings.HasPrefix(boxes[i].Window, statusSpacerPrefix) {
				boxes = slices.Insert(boxes, i, spacerBox())
			}
		}
		return boxes
	}

	// First collect the real views that we want to show, we'll add spacers in
	// between at the end
	var result []*boxlayout.Box

	if !args.InDemo {
		// app status appears very briefly in demos and dislodges the caption,
		// so better not to show it at all
		if args.AppStatus != "" {
			result = append(result, &boxlayout.Box{Window: "appStatus", Size: utils.StringWidth(args.AppStatus)})
		}
	}

	if args.UserConfig.Gui.ShowBottomLine {
		result = append(result, &boxlayout.Box{Window: "options", Weight: 1})
	}

	if (!args.InDemo && args.UserConfig.Gui.ShowBottomLine) || args.IsAnyModeActive {
		result = append(result,
			&boxlayout.Box{
				Window: "information",
				// unlike appStatus, informationStr has various colors so we need to decolorise before taking the length
				Size: utils.StringWidth(utils.Decolorise(args.InformationStr)),
			})
	}

	if len(result) == 2 && result[0].Window == "appStatus" {
		// Only status and information are showing; need to insert a flexible
		// spacer between the two, so that information is right-aligned. Note
		// that the call to insertSpacerBoxes below will still insert a 1-char
		// spacer in addition (right after the flexible one); this is needed for
		// the case that there's not enough room, to ensure there's always at
		// least one space.
		result = slices.Insert(result, 1, flexibleSpacerBox())
	} else if len(result) == 1 {
		if result[0].Window == "information" {
			// Only information is showing; need to add a flexible spacer so
			// that information is right-aligned
			result = slices.Insert(result, 0, flexibleSpacerBox())
		} else {
			// Only status is showing; need to make it flexible so that it
			// extends over the whole width
			result[0].Size = 0
			result[0].Weight = 1
		}
	}

	if len(result) > 0 {
		// If we have at least one view, insert 1-char wide spacer boxes between them.
		result = insertSpacerBoxes(result)
	}

	return result
}

func splitMainPanelSideBySide(args WindowArrangementArgs) bool {
	if args.MainPanes != types.BothMainPanes {
		return false
	}

	mainPanelSplitMode := args.UserConfig.Gui.MainPanelSplitMode
	switch mainPanelSplitMode {
	case "vertical":
		return false
	case "horizontal":
		return true
	default:
		if args.Width < 200 && args.Height > 30 { // 2 80 character width panels + 40 width for side panel
			return false
		}
		return true
	}
}

func getExtrasWindowSize(args WindowArrangementArgs) int {
	var baseSize int
	// The 'extras' window contains the command log context
	if args.CurrentWindow == "extras" {
		baseSize = 1000 // my way of saying 'fill the available space'
	} else if args.Height < 40 {
		baseSize = 1
	} else {
		baseSize = args.UserConfig.Gui.CommandLogSize
	}

	frameSize := 2
	return baseSize + frameSize
}

// A compact side window shows one content line until it is focused, then it
// shares the leftover height. The branches and stash panels use this so the
// files panel can fill the left column. In accordion mode a focused compact
// window uses the expanded weight instead. The window is passed in because
// stash may be a tab of a window named after a different first tab.
func getDefaultStashWindowBox(args WindowArrangementArgs, window string) *boxlayout.Box {
	box := &boxlayout.Box{Window: window}
	// if the window showing stash is focused we should enlargen it
	if args.CurrentSideWindow == window {
		box.Weight = 1
	} else {
		box.Size = 3
	}

	return box
}

func sidePanelChildren(args WindowArrangementArgs) func(width int, height int) []*boxlayout.Box {
	return func(width int, height int) []*boxlayout.Box {
		windows := leftSideWindowNames(args.UserConfig)

		// These thresholds were originally tuned for the default five side panels.
		// With fewer panels there's less to fit, so scale them down proportionally
		// to keep using the proportional layout at smaller heights rather than
		// squashing unnecessarily. We only ever scale down: making more panels
		// squash sooner tends to work against the reason people add panels.
		const defaultSidePanelCount = 5
		minHeightForNormalLayout := min(28, 28*len(windows)/defaultSidePanelCount)
		minHeightForTallSquashedPanels := min(21, 21*len(windows)/defaultSidePanelCount)

		boxForEachWindow := func(boxForWindow func(window string) *boxlayout.Box) []*boxlayout.Box {
			boxes := make([]*boxlayout.Box, 0, len(windows))
			for _, window := range windows {
				boxes = append(boxes, boxForWindow(window))
			}
			return boxes
		}

		if args.ScreenMode == types.SCREEN_FULL || args.ScreenMode == types.SCREEN_HALF {
			fullHeightBox := func(window string) *boxlayout.Box {
				if window == args.CurrentSideWindow {
					return &boxlayout.Box{
						Window: window,
						Weight: 1,
					}
				}

				return &boxlayout.Box{
					Window: window,
					Size:   0,
				}
			}

			return boxForEachWindow(fullHeightBox)
		} else if height >= minHeightForNormalLayout {
			if args.UserConfig.Gui.ShrinkSidePanelsToContent {
				if boxes, ok := shrinkToContentSidePanelBoxes(args, windows, height); ok {
					return boxes
				}
			}

			accordionMode := args.UserConfig.Gui.ExpandFocusedSidePanel
			accordionBox := func(defaultBox *boxlayout.Box) *boxlayout.Box {
				if accordionMode && defaultBox.Window == args.CurrentSideWindow {
					return &boxlayout.Box{
						Window: defaultBox.Window,
						Weight: args.UserConfig.Gui.ExpandedSidePanelWeight,
					}
				}

				return defaultBox
			}

			normalBox := func(window string) *boxlayout.Box {
				// Status and stash sizing follows the tab on screen, not the window
				// name: a window named after stash that is showing another tab keeps
				// that tab's height. The branches window is compact whichever tab it
				// is showing, until the window itself is focused.
				switch args.ActiveViewForWindow(window) {
				case "status":
					// The status view has a fixed height and is not expanded by accordion mode.
					return &boxlayout.Box{Window: window, Size: 3}
				case "stash":
					return accordionBox(getDefaultStashWindowBox(args, window))
				default:
					if window == "branches" {
						return accordionBox(getDefaultStashWindowBox(args, window))
					}
					return accordionBox(&boxlayout.Box{Window: window, Weight: 1})
				}
			}

			return boxForEachWindow(normalBox)
		}

		squashedHeight := 1
		if height >= minHeightForTallSquashedPanels {
			squashedHeight = 3
		}

		squashedSidePanelBox := func(window string) *boxlayout.Box {
			if window == args.CurrentSideWindow {
				return &boxlayout.Box{
					Window: window,
					Weight: 1,
				}
			}

			return &boxlayout.Box{
				Window: window,
				Size:   squashedHeight,
			}
		}

		return boxForEachWindow(squashedSidePanelBox)
	}
}

// shrinkToContentSidePanelBoxes implements the gui.shrinkSidePanelsToContent
// feature: rather than giving every side panel an equal share of the height, we
// size each panel to its own content (plus one blank line, so it's clear there's
// nothing more below), which stops panels with little content from wasting space.
//
// The height freed up by a small panel flows to the panels that have more content
// than their share; those grow up to their own content and then scroll. If every
// panel fits its content with room to spare, there's nothing to absorb the
// leftover, so it's shared among all panels by weight (which, in accordion mode,
// gives the focused panel more of it).
//
// The status panel, the branches panel, and the stash panel when it is not
// focused, keep their constant height and don't take part; ok is false when
// there are no panels to size (so the caller falls back to the normal weighted
// layout).
func shrinkToContentSidePanelBoxes(args WindowArrangementArgs, windows []string, height int) ([]*boxlayout.Box, bool) {
	const frameSize = 2

	accordionMode := args.UserConfig.Gui.ExpandFocusedSidePanel

	// A flexible panel is one we size to its content. Fixed panels (the status
	// panel, the branches panel, and the stash panel when unfocused) get their
	// constant height and are excluded from the distribution below.
	type flexiblePanel struct {
		boxIndex int
		desired  int // target height: content rows (see below) plus the frame
		weight   int
		height   int  // final height, only computed for the room-to-spare case
		capped   bool // true once it fits its content within its share
	}

	boxes := make([]*boxlayout.Box, len(windows))
	flexible := []*flexiblePanel{}
	availableForFlexible := height
	for i, window := range windows {
		focused := window == args.CurrentSideWindow

		// Status and stash sizing follows the tab on screen; the branches window
		// stays compact until focused. See normalBox in sidePanelChildren.
		activeView := args.ActiveViewForWindow(window)
		if activeView == "status" || (activeView == "stash" && !focused) || (window == "branches" && !focused) {
			boxes[i] = &boxlayout.Box{Window: window, Size: 3}
			availableForFlexible -= 3
			continue
		}

		weight := 1
		if accordionMode && focused {
			weight = args.UserConfig.Gui.ExpandedSidePanelWeight
		}
		// Show the content plus a blank line, so it's clear there's nothing more
		// below, but never fewer than two rows: a lone blank row looks cramped,
		// and an empty Files panel is the common state right after launching.
		contentRows := max(args.ContentHeightForWindow(window)+1, 2)
		flexible = append(flexible, &flexiblePanel{
			boxIndex: i,
			desired:  contentRows + frameSize,
			weight:   weight,
		})
	}

	if len(flexible) == 0 || availableForFlexible <= 0 {
		return nil, false
	}

	// Water-filling: repeatedly cap the panels whose desired height is no more
	// than their weighted share of what's left. Capping a panel only raises the
	// others' shares, so this converges once no further panel fits its content.
	// Whatever remains is what the still-uncapped panels have to share.
	remaining := availableForFlexible
	for {
		totalWeight := 0
		for _, p := range flexible {
			if !p.capped {
				totalWeight += p.weight
			}
		}
		if totalWeight == 0 {
			break
		}

		newlyCapped := []*flexiblePanel{}
		for _, p := range flexible {
			if !p.capped && p.desired*totalWeight <= remaining*p.weight {
				newlyCapped = append(newlyCapped, p)
			}
		}
		if len(newlyCapped) == 0 {
			break
		}
		for _, p := range newlyCapped {
			p.capped = true
			remaining -= p.desired
		}
	}

	anyUncapped := false
	for _, p := range flexible {
		if !p.capped {
			anyUncapped = true
		}
	}

	if anyUncapped {
		// Some panels have more content than fits: give the ones that fit exactly
		// their content, and let boxlayout share what's left among the rest by
		// weight (they'll scroll). This is the common, real-world case.
		for _, p := range flexible {
			if p.capped {
				boxes[p.boxIndex] = &boxlayout.Box{Window: windows[p.boxIndex], Size: p.desired}
			} else {
				boxes[p.boxIndex] = &boxlayout.Box{Window: windows[p.boxIndex], Weight: p.weight}
			}
		}
		return boxes, true
	}

	// Every panel fits its content with room to spare, so no panel needs to
	// scroll. Share the leftover equally among them, regardless of focus and
	// accordion mode: enlarging the focused panel here reveals no more content
	// (it already fits) and would only make panels jump around as focus moves.
	// Deal out the rounding remainder one row at a time so the heights fill the
	// available space exactly.
	base := remaining / len(flexible)
	extra := remaining % len(flexible)
	for i, p := range flexible {
		p.height = p.desired + base
		if i < extra {
			p.height++
		}
	}

	// boxlayout can't lay out a set of boxes that are all statically sized (it
	// needs a weighted box to absorb the space), so we hand it the heights as
	// weights: they sum to the available height, so it reproduces them exactly.
	for _, p := range flexible {
		boxes[p.boxIndex] = &boxlayout.Box{Window: windows[p.boxIndex], Weight: p.height}
	}
	return boxes, true
}
