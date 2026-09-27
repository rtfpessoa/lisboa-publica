import type {Stop} from './api';

function stationFor(stop:Stop,catalogue:Map<string,Stop>):Stop{
 const original=catalogue.get(stop.id)??stop;
 if(original.operator_id!==stop.operator_id)return stop;
 let current=original;
 const seen=new Set<string>();
 while(current.parent_id){
  if(seen.has(current.id))return original;
  seen.add(current.id);
  const parent=catalogue.get(current.parent_id);
  if(!parent||parent.operator_id!==original.operator_id)return original;
  current=parent;
 }
 return current;
}

// Published hierarchy establishes station presentation. Names and proximity do
// not establish a parent, and invalid or incomplete chains retain the stop.
export function canonicalStop(stop:Stop,catalogue:Stop[]):Stop{
 return stationFor(stop,new Map(catalogue.map(s=>[s.id,s])));
}

export function canonicalStops(stops:Stop[],catalogue:Stop[]=stops):Stop[]{
 const index=new Map([...stops,...catalogue].map(s=>[s.id,s]));
 const unique=new Map<string,Stop>();
 for(const stop of stops){
  const station=stationFor(stop,index);
  unique.set(station.id,station);
 }
 return [...unique.values()];
}
