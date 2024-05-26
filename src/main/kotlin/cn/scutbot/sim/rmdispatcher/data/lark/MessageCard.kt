package cn.scutbot.sim.rmdispatcher.data.lark

import cn.scutbot.sim.rmdispatcher.data.dji.Match
import com.fasterxml.jackson.annotation.JsonProperty
import com.fasterxml.jackson.module.kotlin.jacksonObjectMapper

data class MessageCard <T>(
    @JsonProperty("type")
    val cardType : String = "template",

    val data : MessageCardData<T>
)

data class MessageCardData<T>(
    @JsonProperty("template_id")
    val templateId: String,

    @JsonProperty("template_variable")
    val templateVariable: T
)

fun <T> newTemplateMessageCard(templateId: String, cardVariable: T) : String {
    val card = MessageCard(data = MessageCardData(templateId, cardVariable))

    return jacksonObjectMapper().writeValueAsString(card)
}

data class CardVariable(
    @JsonProperty("red_team")
    var redTeam: String? = "N/A",

    @JsonProperty("blue_team")
    var blueTeam: String? = "N/A",

    @JsonProperty("match_progress")
    var matchProgress: String? = "N/A",

    @JsonProperty("match_index")
    var matchIndex: String? = "N/A",

    @JsonProperty("total_round")
    var totalRound: String? = "N/A",

    @JsonProperty("match_id")
    var matchId: String? = "N/A",

    @JsonProperty("event_title")
    var eventTitle: String? = "N/A",

    @JsonProperty("red_school")
    var redSchool: String? = "N/A",

    @JsonProperty("blue_school")
    var blueSchool: String? = "N/A",

    @JsonProperty("red_avatar")
    var redAvatar: String? = "img_v2_35d3adee-c47e-4b73-b589-edf4ced99edg",

    @JsonProperty("blue_avatar")
    var blueAvatar: String? = "img_v2_35d3adee-c47e-4b73-b589-edf4ced99edg",

    @JsonProperty("color")
    var color: String? = "red",

    @JsonProperty("match_type")
    var matchType: String? = "N/A",

    @JsonProperty("zone_title")
    var zoneTitle: String? = "N/A",

    @JsonProperty("scores")
    var scores: Set<Score>? = setOf(
        Score("0", "0")
    )
) {
    constructor(match: Match) : this(
        matchId = match.id ?: "",
        redTeam = match.redSide?.player?.team?.name ?: "N/A",
        blueTeam = match.blueSide?.player?.team?.name ?: "N/A",
        redSchool = match.redSide?.player?.team?.collegeName ?: "N/A",
        blueSchool = match.blueSide?.player?.team?.collegeName ?: "N/A",
        matchProgress = "开始",
        matchIndex = match.orderNumber?.toString() ?: "N/A",
        totalRound = match.totalRound?.toString() ?: "N/A",
        eventTitle = match.zone?.event?.title ?: "N/A",
        zoneTitle = match.zone?.name ?: "N/A",
        color = "yellow",
        matchType = match.matchType ?: "N/A")
}

data class Score(
    @JsonProperty("red_score")
    var redScore: String? = "0",

    @JsonProperty("blue_score")
    var blueScore: String? = "0"
)