# -*- coding: utf-8 -*-
#
# Copyright (c) 2014+ Tyurin Anton noxiouz@yandex.ru
#
# This file is part of Combaine.
#
# Combaine is free software; you can redistribute it and/or modify
# it under the terms of the GNU Lesser General Public License as published by
# the Free Software Foundation; either version 3 of the License, or
# (at your option) any later version.
#
# Combaine is distributed in the hope that it will be useful,
# but WITHOUT ANY WARRANTY; without even the implied warranty of
# MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
# GNU Lesser General Public License for more details.
#
# You should have received a copy of the GNU Lesser General Public License
# along with this program. If not, see <http://www.gnu.org/licenses/>.
#

import logging

from cocaine.logging.hanlders import CocaineHandler

l = logging.getLogger("combaine")
ch = CocaineHandler()
formatter = logging.Formatter("%(tid)s %(message)s")
ch.setFormatter(formatter)
ch.setLevel(logging.DEBUG)
l.addHandler(ch)


def get_logger_adapter(tid):
    return logging.LoggerAdapter(l, {"tid": tid})
