import ReactEChartsCore from 'echarts-for-react/esm/core'
import type { EChartsOption } from 'echarts'
import { echarts } from '@/lib/echarts'

/** Thin wrapper around echarts-for-react using our tree-shaken ECharts build. */
export function EChart({ option, className }: { option: EChartsOption; className?: string }) {
  return (
    <ReactEChartsCore
      echarts={echarts}
      option={option}
      notMerge={false}
      lazyUpdate
      className={className}
      style={{ width: '100%', height: '100%' }}
      opts={{ renderer: 'canvas' }}
    />
  )
}
