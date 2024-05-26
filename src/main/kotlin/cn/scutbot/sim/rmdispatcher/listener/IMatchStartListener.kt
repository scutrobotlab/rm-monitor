package cn.scutbot.sim.rmdispatcher.listener

import cn.scutbot.sim.rmdispatcher.data.dji.Match

interface IMatchStartListener {
    fun onMatchStart(match: Match)
}