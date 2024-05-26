package cn.scutbot.sim.rmdispatcher.listener

import cn.scutbot.sim.rmdispatcher.data.dji.Match

interface IMatchSessionEndListener {
    fun onMatchSessionEnd(match: Match)
}