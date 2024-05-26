package cn.scutbot.sim.rmdispatcher.filter

import cn.scutbot.sim.rmdispatcher.data.dji.DjiFilterContext
import cn.scutbot.sim.rmdispatcher.listener.IMatchSessionEndListener
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.stereotype.Component

@Component
class MatchSessionEndFilter(
    @Autowired val listeners: List<IMatchSessionEndListener>
) : IDjiInfoFilter {
    override fun filter(context: DjiFilterContext): DjiFilterContext {
        val match = context.match

        listeners.forEach {
            it.onMatchSessionEnd(match)
        }

        return context
    }

    override fun condition(context: DjiFilterContext): Boolean {
        return  context.meta["emptyCurrent"] == false && context.meta["nameEquals"] == true && context.meta["scoreEquals"] == false
    }
}