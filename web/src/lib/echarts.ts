// Tree-shaken ECharts build: only the pieces our widgets use.
import * as echarts from 'echarts/core'
import { GaugeChart, LineChart } from 'echarts/charts'
import { GridComponent, MarkLineComponent, TooltipComponent, MarkAreaComponent } from 'echarts/components'
import { CanvasRenderer } from 'echarts/renderers'

echarts.use([
  GaugeChart,
  LineChart,
  GridComponent,
  TooltipComponent,
  MarkLineComponent,
  MarkAreaComponent,
  CanvasRenderer,
])

export { echarts }
