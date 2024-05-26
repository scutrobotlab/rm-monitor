package cn.scutbot.sim.rmdispatcher.data.dji

import com.fasterxml.jackson.annotation.JsonIgnoreProperties

@JsonIgnoreProperties(ignoreUnknown = true)
data class Match(
    val id: String? = null,
    val groupId: String? = null,
    val round: Int? = null,
    val totalRound: Int? = null,
    val orderNumber: Int? = null,
    val matchType: String? = null,
    val blueSideId: String? = null,
    val blueSide: Side? = null,
    val redSideId: String? = null,
    val redSide: Side? = null,
    val redSideScore: Int? = null,
    val blueSideScore: Int? = null,
    val blueSideWinGameCount: Int? = null,
    val planGameCount: Int? = null,
    val planStartedAt: String? = null,
    val redSideWinGameCount: Int? = null,
    val winnerPlaceholdName: String? = null,
    val loserPlaceholdName: String? = null,
    val slug: String? = null,
    val slugName: String? = null,
    val status: String? = null,
    var zone: Zone? = null,
    val result: String? = null
) {
    fun nameEquals(other: Any?): Boolean {
        if (other !is Match)
            return false

        return blueSide?.player?.team?.collegeName == other.blueSide?.player?.team?.collegeName
                && redSide?.player?.team?.collegeName == other.redSide?.player?.team?.collegeName
    }

    fun scoreEquals(other: Any?) : Boolean {
        if (other !is Match)
            return false

        return round == other.round
    }

    companion object {
        val EMPTY: Match = Match()
    }
}

