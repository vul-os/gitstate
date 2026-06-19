/**
 * Landing — thin composition page.
 * Each section is independently owned in pages/landing/.
 *
 * Sections (in order):
 *   Hero          hero visual + headline + CTAs + live git-graph motif
 *   TrustBand     honest "works with" platform/stack strip
 *   Disciplines   the five honest constraints
 *   DerivedDemo   ticket-vs-diff side-by-side
 *   Features      six key capabilities grid
 *   Capabilities  alternating screenshot showcase (7 caps)
 *   Stats         four proof numbers strip
 *   ICP           client-billing dev shops callout
 *   CompareTeaser gitstate vs Jira vs Linear table
 *   FAQ           honest answers accordion
 *   FinalCTA      closing headline + actions
 */
import MarketingLayout from '../components/marketing/MarketingLayout.jsx'
import Hero from './landing/Hero.jsx'
import TrustBand from './landing/TrustBand.jsx'
import Disciplines from './landing/Disciplines.jsx'
import DerivedDemo from './landing/DerivedDemo.jsx'
import Features from './landing/Features.jsx'
import Capabilities from './landing/Capabilities.jsx'
import Stats from './landing/Stats.jsx'
import ICP from './landing/ICP.jsx'
import CompareTeaser from './landing/CompareTeaser.jsx'
import FAQ from './landing/FAQ.jsx'
import FinalCTA from './landing/FinalCTA.jsx'

export default function Landing() {
  return (
    <MarketingLayout>
      <Hero />
      <TrustBand />
      <Disciplines />
      <DerivedDemo />
      <Features />
      <Capabilities />
      <Stats />
      <ICP />
      <CompareTeaser />
      <FAQ />
      <FinalCTA />
    </MarketingLayout>
  )
}
