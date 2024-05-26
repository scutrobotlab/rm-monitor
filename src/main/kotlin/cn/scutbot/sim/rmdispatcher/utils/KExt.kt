package cn.scutbot.sim.rmdispatcher.utils

import org.slf4j.Logger
import org.slf4j.LoggerFactory
import java.util.*

inline fun <reified T> T.logger(): Logger =
    LoggerFactory.getLogger(T::class.java)

fun randId(len: Int): String =
    UUID.randomUUID().toString().uppercase().substring(0, len)
