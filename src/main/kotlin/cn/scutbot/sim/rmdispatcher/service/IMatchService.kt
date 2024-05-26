package cn.scutbot.sim.rmdispatcher.service

import cn.scutbot.sim.rmdispatcher.data.lark.CardVariable

interface IMatchService {
    fun getMessageIds(matchId: String) : Set<String>

    fun getMatchCardVars(matchId: String) : CardVariable?

    fun saveMessageIds(matchId: String, messageIds: Set<String>)

    fun saveMatchCardVars(matchId: String, vars: CardVariable)
}