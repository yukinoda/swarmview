"""Finder layout for dmgbuild; artwork is rendered separately with AppKit."""

app = defines["app"]
files = [(app, "Swarmview.app")]
symlinks = {"Applications": "/Applications"}
format = "UDZO"
icon = defines["icon"]
background = defines["background"]
window_rect = ((160, 160), (760, 508))
icon_size = 116
text_size = 14
icon_locations = {"Swarmview.app": (190, 256), "Applications": (570, 256)}
default_view = "icon-view"
show_toolbar = False
show_tab_view = False
show_status_bar = False
show_pathbar = False
show_sidebar = False
include_icon_view_settings = True
include_list_view_settings = False
arrange_by = None
grid_spacing = 80
scroll_position = (0, 0)
show_icon_preview = False
