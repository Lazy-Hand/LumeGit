---
name: lobe-mygo
description: "Lobe UI components for MyGo desktop apps, drawn on the GPU in pure Go. Use when choosing a component, looking up its props and API, or implementing UI with it."
---

# Lobe MyGo

> Lobe UI components for MyGo desktop apps, drawn on the GPU in pure Go.

## Setup

- Repository: https://github.com/lobehub/lobe-mygo
- Docs: https://mygo.lobehub.com/

## Workflow

1. Pick candidates from the index below. Groups follow the source folder, which usually maps to the import entry.
2. Fetch the linked reference page before writing code. It holds the documented usage, props and API tables.
3. Only use props, variants and imports that the reference page documents. Follow its Docs link to see live demos.

The page index with outlines is at https://mygo.lobehub.com/llms.txt.

## Index

### Guides

- [Getting Started](https://mygo.lobehub.com/skills/getting-started.md) — Add Lobe MyGo to a Go module, open a window with a Lobe UI button, and find the components by package.

### catalog

#### Data Display

- [Accordion](https://mygo.lobehub.com/skills/components/accordion.md) — Sections that open and close below their headers, borderless, filled or outlined, with an arrow before or after the title.
- [AgentAvatar](https://mygo.lobehub.com/skills/components/agent-avatar.md) — An agent drawn as a flat silhouette with the logo's eyes, its shape, color and lean seeded by its name, its state shown by its eyes and pos…
- [Avatar](https://mygo.lobehub.com/skills/components/avatar.md) — A user or agent avatar in the Lobe UI look, showing an image, an emoji, letters, or marbled colors seeded by the name.
- [Empty](https://mygo.lobehub.com/skills/components/empty.md) — A placeholder for a list or page with nothing in it yet, with an icon, title, description and actions.
- [GroupAvatar](https://mygo.lobehub.com/skills/components/group-avatar.md) — A group shown by its members' avatars in a grid, as a group chat in a list.
- [Highlighter](https://mygo.lobehub.com/skills/components/highlighter.md) — A block of code highlighted in Lobe's code colors, with a copy button and the language on hover, and a full-featured header that collapses…
- [Hotkey](https://mygo.lobehub.com/skills/components/hotkey.md) — A keyboard shortcut drawn as key caps, named for Apple devices with ⌘ ⌥ ⇧ glyphs and for others with Ctrl, Alt and Shift.
- [Image](https://mygo.lobehub.com/skills/components/image.md) — A picture in a rounded frame that opens in a preview viewer, which zooms with the wheel, a pinch, a double click or its toolbar and pans wh…
- [Markdown](https://mygo.lobehub.com/skills/components/markdown.md) — Markdown in Lobe's typography, with default and chat variants and code blocks through Highlighter and Snippet.
- [Popover](https://mygo.lobehub.com/skills/components/popover.md) — A floating panel in the Lobe UI look that opens from an element on click and closes on a press outside or Escape.
- [QRCode](https://mygo.lobehub.com/skills/components/qr-code.md) — A QR code on a white card, with an optional logo over its middle and loading or expired states.
- [Skeleton](https://mygo.lobehub.com/skills/components/skeleton.md) — Placeholder blocks for loading content, as single blocks, avatars and lines of text, that pulse or sweep.
- [Snippet](https://mygo.lobehub.com/skills/components/snippet.md) — A line of code, as a command to run, highlighted with a prefix and a copy button.
- [SortableList](https://mygo.lobehub.com/skills/components/sortable-list.md) — Rows that drag into a new order by a handle, making room as the dragged row passes, and reorder from the keyboard.
- [Tag](https://mygo.lobehub.com/skills/components/tag.md) — A small label in the Lobe UI look, in preset palettes, status colors or any hex color, with four variants, three sizes and a close button.
- [Tooltip](https://mygo.lobehub.com/skills/components/tooltip.md) — A short hint in the Lobe UI look that shows by an element once the pointer rests on it, springing in toward its anchor.

#### Data Entry

- [AutoComplete](https://mygo.lobehub.com/skills/components/auto-complete.md) — An Input that suggests options below it as the user types, matching what was typed, with an empty state.
- [Checkbox](https://mygo.lobehub.com/skills/components/checkbox.md) — A check box in the Lobe UI look, square or round, with an indeterminate state for a box over a group.
- [ColorPicker](https://mygo.lobehub.com/skills/components/color-picker.md) — A color picker in a popover, with a saturation area, hue and alpha sliders, a hex field, presets, and three triggers.
- [ColorSwatches](https://mygo.lobehub.com/skills/components/color-swatches.md) — A row of color swatches choosing one color, round or square, with a check on the chosen one and a tooltip for each.
- [CopyButton](https://mygo.lobehub.com/skills/components/copy-button.md) — An ActionIcon that copies text to the clipboard and shows a check for two seconds after.
- [DatePicker](https://mygo.lobehub.com/skills/components/date-picker.md) — A date field that opens a calendar with month and year views, a Today button, disabled dates and keyboard navigation.
- [EditableText](https://mygo.lobehub.com/skills/components/editable-text.md) — Text with an edit button that turns into a field, saved with Enter and dropped with Escape.
- [Form](https://mygo.lobehub.com/skills/components/form.md) — Settings-style groups and fields over values the app holds, with required and custom checks, async validation, dependent fields, unsaved ch…
- [HotkeyInput](https://mygo.lobehub.com/skills/components/hotkey-input.md) — A field that records a keyboard shortcut, refusing combinations without a modifier and shortcuts taken elsewhere.
- [ImageSelect](https://mygo.lobehub.com/skills/components/image-select.md) — Chooses one option from a row of pictures with labels, as a theme or layout picker, ringing the chosen picture.
- [Input](https://mygo.lobehub.com/skills/components/input.md) — A one-line text input in the Lobe UI look, outlined, filled or borderless, with icons before and after the text and an invalid state.
- [InputNumber](https://mygo.lobehub.com/skills/components/input-number.md) — A number field in the Input look, with step arrows at the end, bounds, a step and a fixed precision.
- [InputOTP](https://mygo.lobehub.com/skills/components/input-otp.md) — A one-time code field in a row of cells, one character each, that runs a callback once the code is complete.
- [InputPassword](https://mygo.lobehub.com/skills/components/input-password.md) — A password field in the Input look that hides what it holds, with an eye button that shows it.
- [Radio](https://mygo.lobehub.com/skills/components/radio.md) — A radio button in the Lobe UI look, choosing one value of a group, with the arrows moving between the choices.
- [Rate](https://mygo.lobehub.com/skills/components/rate.md) — A star rating in the Lobe UI look, with half stars, a hover preview, custom characters and keyboard control.
- [SearchBar](https://mygo.lobehub.com/skills/components/search-bar.md) — A search input with a search icon, a clear button, a loading state and a window-wide shortcut shown as a key tag.
- [Segmented](https://mygo.lobehub.com/skills/components/segmented.md) — Segments choosing one of a few options in the Lobe UI look, whose indicator slides to the chosen one.
- [Select](https://mygo.lobehub.com/skills/components/select.md) — A drop-down choosing one option, with a trigger in the Input look and a popup that marks the chosen option.
- [Slider](https://mygo.lobehub.com/skills/components/slider.md) — A slider in the Lobe UI look, with a thin track, a pill thumb that rings on hover, and steps that snap the value.
- [Switch](https://mygo.lobehub.com/skills/components/switch.md) — A switch in the Lobe UI look whose thumb moves on Lobe UI's spring and stretches while pressed.
- [TextArea](https://mygo.lobehub.com/skills/components/text-area.md) — A multi-line text input in the Lobe UI look that grows with its text between a minimum and a maximum number of lines.
- [ToggleGroup](https://mygo.lobehub.com/skills/components/toggle-group.md) — Toggle buttons pressed one at a time or many at once, joined in a border, spaced apart, or on a segmented track.
- [Upload](https://mygo.lobehub.com/skills/components/upload.md) — A trigger or a dashed drop zone that opens the system file picker, filters files by accept, BeforeUpload and MaxCount, and hands the files…

#### Feedback

- [Alert](https://mygo.lobehub.com/skills/components/alert.md) — A message banner in the Lobe UI look, tinted by its type, with an optional description and close button.
- [Badge](https://mygo.lobehub.com/skills/components/badge.md) — A count or dot on the corner of an icon or avatar, or a status dot with text.
- [Drawer](https://mygo.lobehub.com/skills/components/drawer.md) — A panel in the Lobe UI look that slides in from an edge of the window over a dimmed backdrop, with a header, body and footer.
- [FloatingPanel](https://mygo.lobehub.com/skills/components/floating-panel.md) — A panel floating at a corner of the window beside the page, for side conversations, task runs and inspectors, resizable from its free edges.
- [FloatingSheet](https://mygo.lobehub.com/skills/components/floating-sheet.md) — A bottom sheet inside its container that drags taller or shorter by its header, snaps to points, and drags down to close.
- [Modal](https://mygo.lobehub.com/skills/components/modal.md) — A dialog in the Lobe UI look over a frosted backdrop, with a title, a close button and OK and Cancel actions.
- [Progress](https://mygo.lobehub.com/skills/components/progress.md) — A line or circle showing how far along a task is, with status colors, a moving shine, segments and an inset pill.
- [Result](https://mygo.lobehub.com/skills/components/result.md) — The outcome of an operation, with a status icon in a tinted circle, a title, a subtitle and actions.
- [Spin](https://mygo.lobehub.com/skills/components/spin.md) — A loading indicator, turning or as a network of pulsing nodes, alone or over dimmed content with a tip.
- [Toast](https://mygo.lobehub.com/skills/components/toast.md) — Brief notifications in the Lobe UI look that stack in a corner, peek behind the newest, and spread out under the pointer.

#### Foundation

- [Motion](https://mygo.lobehub.com/skills/components/motion.md) — Lobe UI's motion tokens and presets for MyGo, springs set by damping and settle time, and the enter and exit transitions of popups, tooltip…
- [Tokens](https://mygo.lobehub.com/skills/components/tokens.md) — The Lobe UI color tokens generated into Go, drawn by MyGo in the current theme.

#### General

- [ActionIcon](https://mygo.lobehub.com/skills/components/action-icon.md) — An icon-only button in the Lobe UI look, with borderless, filled and outlined surfaces, an active state and a danger color.
- [ActionIconGroup](https://mygo.lobehub.com/skills/components/action-icon-group.md) — A row of icon buttons on one surface, as the actions under a chat message, with an optional More menu.
- [Block](https://mygo.lobehub.com/skills/components/block.md) — A rounded surface in the filled, outlined or borderless Lobe UI look, optionally clickable with a hover state and a soft shadow.
- [Button](https://mygo.lobehub.com/skills/components/button.md) — A push button in the Lobe UI look, built on MyGo's ButtonBase, with six variants and three sizes.
- [Icon](https://mygo.lobehub.com/skills/components/icon.md) — An SVG icon from the generated lucide package, sized 14, 20 or 24 DIPs or following the font size, optionally spinning.

#### Layout

- [Divider](https://mygo.lobehub.com/skills/components/divider.md) — A thin separator line in the Lobe UI look, horizontal or vertical, solid or dashed, with an optional centered label.
- [DraggablePanel](https://mygo.lobehub.com/skills/components/draggable-panel.md) — A panel at an edge of its container that resizes by dragging its seam and collapses with a button on it or by dragging it small.
- [DraggableSideNav](https://mygo.lobehub.com/skills/components/draggable-side-nav.md) — A side navigation that resizes by dragging its edge, collapses to a slim rail, and tells its content which way it shows.
- [Grid](https://mygo.lobehub.com/skills/components/grid.md) — A responsive grid that fits as many equal columns as its width allows, each at least a set width, up to a column cap.
- [GuideCard](https://mygo.lobehub.com/skills/components/guide-card.md) — A dismissible tip with a cover, a title and a description, as a feature's introduction.
- [Header](https://mygo.lobehub.com/skills/components/header.md) — A site's 64 DIP top bar with a logo, links and actions over a line, and a mobile arrangement.
- [Layout](https://mygo.lobehub.com/skills/components/layout.md) — A documentation page with a header over a resizable sidebar, the content and its table of contents, and a footer.
- [ScrollArea](https://mygo.lobehub.com/skills/components/scroll-area.md) — A scroll container with Lobe UI's thin scroll bars and edges that fade where there is more to scroll to.
- [SideNav](https://mygo.lobehub.com/skills/components/side-nav.md) — An app's 58 DIP rail of icon buttons along the left edge, with an avatar at the top and actions at the bottom.

#### Navigation

- [Breadcrumb](https://mygo.lobehub.com/skills/components/breadcrumb.md) — The path to the current page, with each earlier item a button and the current page last in text color.
- [Burger](https://mygo.lobehub.com/skills/components/burger.md) — A mobile header's menu button that opens a drawer of rows from the top, or a fullscreen menu.
- [ContextMenu](https://mygo.lobehub.com/skills/components/context-menu.md) — A menu in the Lobe UI look that opens where an element is right-clicked, with the same items as DropdownMenu.
- [DropdownMenu](https://mygo.lobehub.com/skills/components/dropdown-menu.md) — A menu in the Lobe UI look that opens below a trigger, with icons, shortcuts, dividers, danger and disabled items.
- [List](https://mygo.lobehub.com/skills/components/list.md) — Rows for navigation and settings with an icon or avatar, a label, a description, extra text and actions that show on hover.
- [Pagination](https://mygo.lobehub.com/skills/components/pagination.md) — A pager in the Lobe UI look with previous and next buttons, the pages around the current one, and ellipses that jump several pages.
- [Steps](https://mygo.lobehub.com/skills/components/steps.md) — The steps of a process as numbered circles or dots, linked by connectors that turn primary after the finished steps.
- [Tabs](https://mygo.lobehub.com/skills/components/tabs.md) — Tabs in the Lobe UI look with an indicator that slides to the chosen tab, in rounded, square and point variants.

#### Typography

- [Text](https://mygo.lobehub.com/skills/components/text.md) — Text in the Lobe UI type styles, with status colors, headings, marks, an ellipsis after a number of lines and a shiny sweep for pending sta…

