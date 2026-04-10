import type { CSSProperties } from "react";
import { motion } from "motion/react";
import type { DashboardDecorOrbConfig } from "../dashboardHome.types";

type DashboardDecorOrbProps = {
  config: DashboardDecorOrbConfig;
  dimmed: boolean;
  offset: { x: number; y: number };
  rotationAngle: number;
};

export function DashboardDecorOrb({ config, dimmed, offset, rotationAngle }: DashboardDecorOrbProps) {
  const rad = (rotationAngle * Math.PI) / 180;
  const x = Math.cos(rad) * config.orbitRadius + offset.x * 0.08;
  const y = Math.sin(rad) * config.orbitRadius + offset.y * 0.08;
  const style = {
    background: config.color,
    boxShadow: `0 0 18px ${config.glow}`,
    height: `${config.size}px`,
    left: `calc(50% + ${x}px)`,
    top: `calc(50% + ${y}px)`,
    width: `${config.size}px`,
  } as CSSProperties;

  return <motion.span animate={{ opacity: dimmed ? 0.12 : 0.46, scale: dimmed ? 0.9 : 1 }} className="dashboard-orbit-decor" style={style} transition={{ duration: 0.3 }} />;
}
