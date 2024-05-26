package cn.scutbot.sim.rmdispatcher.filter

import cn.scutbot.sim.rmdispatcher.data.dji.DjiFilterContext
import cn.scutbot.sim.rmdispatcher.data.dji.Match
import cn.scutbot.sim.rmdispatcher.listener.IMatchEndListener
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.stereotype.Component

@Component
class MatchEndFilter(
    @Autowired val listeners: List<IMatchEndListener>
) : IDjiInfoFilter {
    override fun filter(context: DjiFilterContext): DjiFilterContext {
        val previous = context.meta["previous"] as Match

        listeners.forEach {
            it.onMatchEnd(previous)
        }

        return context
    }

    override fun condition(context: DjiFilterContext): Boolean {
        return context.meta["nameEquals"] == false && context.meta["emptyPrevious"] == false
    }
}