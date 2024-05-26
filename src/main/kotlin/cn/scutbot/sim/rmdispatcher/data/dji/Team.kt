package cn.scutbot.sim.rmdispatcher.data.dji

import com.fasterxml.jackson.annotation.JsonIgnoreProperties

@JsonIgnoreProperties(ignoreUnknown = true)
data class Team(
    val id: String? = null,
    val collegeLogo: String? = null,
    val collegeName: String? = null,
    val name: String? = null
)
