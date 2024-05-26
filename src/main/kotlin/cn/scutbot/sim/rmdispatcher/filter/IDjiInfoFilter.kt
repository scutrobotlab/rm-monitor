package cn.scutbot.sim.rmdispatcher.filter

import cn.scutbot.sim.rmdispatcher.data.dji.DjiFilterContext

interface IDjiInfoFilter {
    fun filter(context: DjiFilterContext): DjiFilterContext

    fun condition(context: DjiFilterContext) : Boolean
}
