package cn.scutbot.sim.rmdispatcher.filter

import cn.scutbot.sim.rmdispatcher.data.dji.DjiFilterContext
import cn.scutbot.sim.rmdispatcher.data.dji.Match
import cn.scutbot.sim.rmdispatcher.listener.IMatchStartListener
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.stereotype.Component

@Component
class MatchStartFilter(
    @Autowired val listeners: List<IMatchStartListener>
) : IDjiInfoFilter {
    override fun condition(context: DjiFilterContext): Boolean {
        return context.Get<Boolean>("nameEquals") == false && !context.match.nameEquals(Match.EMPTY)
    }

    override fun filter(context: DjiFilterContext): DjiFilterContext {
        listeners.forEach {
            it.onMatchStart(context.match)
        }

        return context
    }
}