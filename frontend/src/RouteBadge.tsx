import type {Route} from './api';
import {routeName} from './data';
export default function RouteBadge({route}:{route:Route}){
 const title=routeName(route.operator_id,route.short_name);
 return <b className={route.operator_id==='metro'?'metro-line-badge':''} style={{background:route.color}} title={title} aria-label={title}>{route.operator_id==='metro'?route.short_name:title}</b>;
}
