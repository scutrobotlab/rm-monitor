package cn.scutbot.sim.rmdispatcher.service

import cn.scutbot.sim.rmdispatcher.data.dji.CurrentAndNextMatch
import cn.scutbot.sim.rmdispatcher.data.dji.Match

interface IDjiService {
    fun fetchInfo(): List<CurrentAndNextMatch>

    fun fetchSchedule(match: Match) : Match?

    fun collegeFullNames() : List<String>

    fun zones() : List<String>
}
