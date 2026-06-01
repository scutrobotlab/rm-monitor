{
    "schema": "2.0",
    "config": {
        "enable_forward": true,
        "update_multi": true,
        "width_mode": "compact",
        "enable_forward_interaction": false,
        "style": {
            "text_size": {
                "normal_v2": {
                    "default": "normal",
                    "pc": "normal",
                    "mobile": "heading"
                }
            }
        }
    },
    "body": {
        "direction": "vertical",
        "horizontal_spacing": "8px",
        "vertical_spacing": "2px",
        "horizontal_align": "left",
        "vertical_align": "top",
        "padding": "12px 12px 12px 12px",
        "elements": [
            {
                "tag": "column_set",
                "horizontal_spacing": "0px",
                "horizontal_align": "center",
                "columns": [
                    {
                        "tag": "column",
                        "width": "weighted",
                        "elements": [
                            {{- if .RedAvatar }}
                            {
                                "tag": "img",
                                "img_key": {{json .RedAvatar}},
                                "preview": true,
                                "transparent": false,
                                "scale_type": "fit_horizontal",
                                "margin": "0px 0px 0px 0px"
                            },
                            {{- end }}
                            {
                                "tag": "markdown",
                                "content": "### <font color=red>**{{jsonText .RedSchool}}**</font> \n <font color=red>{{jsonText .RedTeam}}</font>",
                                "text_align": "center",
                                "text_size": "normal_v2",
                                "margin": "0px 0px 0px 0px"
                            }
                        ],
                        "vertical_spacing": "8px",
                        "horizontal_align": "left",
                        "vertical_align": "top",
                        "weight": 5
                    },
                    {
                        "tag": "column",
                        "width": "weighted",
                        "elements": [
                            {
                                "tag": "div",
                                "text": {
                                    "tag": "plain_text",
                                    "content": "VS",
                                    "text_size": "heading",
                                    "text_align": "center",
                                    "text_color": "default"
                                },
                                "margin": "0px 0px 0px 0px"
                            }
                        ],
                        "padding": "0px 0px 0px 0px",
                        "direction": "vertical",
                        "horizontal_spacing": "8px",
                        "vertical_spacing": "8px",
                        "horizontal_align": "center",
                        "vertical_align": "center",
                        "margin": "0px 0px 0px 0px",
                        "weight": 1
                    },
                    {
                        "tag": "column",
                        "width": "weighted",
                        "elements": [
                            {{- if .BlueAvatar }}
                            {
                                "tag": "img",
                                "img_key": {{json .BlueAvatar}},
                                "preview": true,
                                "transparent": false,
                                "scale_type": "fit_horizontal",
                                "margin": "0px 0px 0px 0px"
                            },
                            {{- end }}
                            {
                                "tag": "markdown",
                                "content": "### <font color=blue>**{{jsonText .BlueSchool}}**</font>\n <font color=blue>{{jsonText .BlueTeam}}</font>",
                                "text_align": "center",
                                "text_size": "normal_v2",
                                "margin": "0px 0px 0px 0px"
                            }
                        ],
                        "vertical_spacing": "8px",
                        "horizontal_align": "left",
                        "vertical_align": "top",
                        "weight": 5
                    }
                ],
                "margin": "0px 0px 0px 0px"
            },
            {{- range $i, $round := .Rounds }}
            {
                "tag": "collapsible_panel",
                "element_id": {{json $round.PanelID}},
                "expanded": true,
                "direction": "vertical",
                "horizontal_align": "center",
                "vertical_align": "center",
                "background_color": "grey-200",
                "header": {
                    "title": {
                        "tag": "markdown",
                        "content": {{json $round.Title}},
                        "text_align": "center",
                        "text_size": "normal_v2"
                    },
                    "icon": {
                        "tag": "standard_icon",
                        "token": "down-small-ccm_outlined",
                        "color": "",
                        "size": "16px 16px"
                    },
                    "vertical_align": "center",
                    "padding": "4px 0px 4px 0px",
                    "position": "top",
                    "width": "fill"
                },
                "border": {
                    "corner_radius": "5px"
                },
                "elements": [
                    {
                        "tag": "markdown",
                        "element_id": {{json $round.ContentID}},
                        "content": {{json $round.Content}},
                        "text_align": "left",
                        "text_size": "normal_v2",
                        "margin": "0px 0px 0px 0px"
                    }{{- if $round.SettlementImageKey }},
                    {
                        "tag": "img",
                        "img_key": {{json $round.SettlementImageKey}},
                        "preview": true,
                        "transparent": false,
                        "scale_type": "fit_horizontal",
                        "margin": "6px 0px 0px 0px"
                    }
                    {{- end }}
                ],
                "padding": "8px 8px 8px 8px",
                "margin": "4px 0px 4px 0px"
            },
            {{- end }}
            {
                "tag": "markdown",
                "content": "{{jsonText .Report}}",
                "text_align": "left",
                "text_size": "normal_v2",
                "margin": "4px 0px 4px 0px"
            },
            {{- if .HighlightMarkdown }}
            {
                "tag": "markdown",
                "element_id": "featured_highlights",
                "content": {{json .HighlightMarkdown}},
                "text_align": "left",
                "text_size": "normal_v2",
                "margin": "4px 0px 4px 0px"
            },
            {{- end }}
            {{- if eq (len .HighlightImages) 1 }}
            {{- $img := index .HighlightImages 0 }}
            {
                "tag": "img",
                "element_id": "highlight_image_1",
                "img_key": {{json $img.ImageKey}},
                "preview": true,
                "transparent": false,
                "scale_type": "fit_horizontal",
                "margin": "6px 0px 6px 0px"
            },
            {{- else if .HighlightImages }}
            {
                "tag": "img_combination",
                "element_id": "highlight_images",
                "combination_mode": {{json .HighlightMode}},
                "combination_transparent": false,
                "corner_radius": "8px",
                "margin": "6px 0px 6px 0px",
                "img_list": [
                    {{- range $i, $img := .HighlightImages }}
                    {{- if $i }},{{ end }}
                    {
                        "img_key": {{json $img.ImageKey}},
                        "transparent": false
                    }
                    {{- end }}
                ]
            },
            {{- end }}
            {
                "tag": "div",
                "text": {
                    "tag": "plain_text",
                    "content": "{{jsonText .EventTitle}} ",
                    "text_size": "notation",
                    "text_align": "left",
                    "text_color": "grey"
                },
                "icon": {
                    "tag": "standard_icon",
                    "token": "tab-video_filled",
                    "color": "black"
                }
            }
        ]
    },
    "header": {
        "title": {
            "tag": "plain_text",
            "content": "{{jsonText .MatchIndex}}. {{jsonText .RedTeam}} VS {{jsonText .BlueTeam}}"
        },
        "subtitle": {
            "tag": "plain_text",
            "content": "{{jsonText .MatchProgress}}"
        },
        "text_tag_list": [
            {
                "tag": "text_tag",
                "text": {
                    "tag": "plain_text",
                    "content": "BO{{jsonText .TotalRound}}"
                },
                "color": "lime"
            },
            {
                "tag": "text_tag",
                "text": {
                    "tag": "plain_text",
                    "content": "{{jsonText .ZoneTitle}}"
                },
                "color": "turquoise"
            },
            {
                "tag": "text_tag",
                "text": {
                    "tag": "plain_text",
                    "content": "{{jsonText .MatchType}}"
                },
                "color": "carmine"
            }
        ],
        "template": {{json .Color}},
        "padding": "12px 12px 12px 12px"
    }
}
