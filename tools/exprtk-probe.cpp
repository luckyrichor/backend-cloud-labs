#include "exprtk.hpp"
#include <iostream>
int main(){double x=2;exprtk::symbol_table<double> symbols;symbols.add_variable("x",x);exprtk::expression<double> e;e.register_symbol_table(symbols);exprtk::parser<double> p;if(!p.compile("x*2+1",e))return 2;double v=e.value();std::cout<<v<<"\n";return v==5?0:3;}
