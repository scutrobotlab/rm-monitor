package cn.scutbot.sim.rmdispatcher.service

interface IRecorderService {
    fun clip(roomId: Int)

    fun start(roomId: Int)

    fun stop(roomId: Int)
}
